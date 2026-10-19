import { lazy, Suspense, useCallback, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Badge, Button, Icon, IconButton, Stack, Text, TextArea, useStyles2 } from '@grafana/ui'
import { Code } from '@connectrpc/connect'
import { GoFiMark } from '../GoFiMark'
import {
  AssistantError, chat, confirmAssistantAction, errorMessage, getAssistantConversation, listAssistantConversations, resetConversation,
  type ChatDone, type ConversationSummary,
} from '../../assistant-api'
import { navigate } from '../../router'
import { formatNumber, formatRelativeTime, t } from '../../i18n'
import {
  addHighlight, addNavigation, addProposal, addToolCall, addToolResult, appendText, decideProposal, decideToolCall, failProposal, failToolCall, finishTurn,
  loadConversationId, nextTurnId, saveConversationId, startExchange, turnsFromTranscript, type AssistantTurn, type Proposal, type ToolActivity, type Turn,
} from './conversation'
import { fieldLanguage, outputLanguage, pageContextOf, proposalInput, summarizeInput, toolInputView, truncateOutput } from './helpers'
import { Markdown } from './Markdown'
import { CodeBlock } from './CodeBlock'
import { FloatingWindow } from './FloatingWindow'
import { ResizableSplit } from '../canvas/ResizableSplit'
import { ASSISTANT_OPEN_EVENT, type AssistantOpenDetail } from './useAssistantReadiness'

const FlowHighlightCard = lazy(() => import('./FlowHighlightCard'))

const SHARE_KEY = 'gofi.assistant.share'
const WINDOW_KEY = 'gofi.assistant.window'
const MODE_KEY = 'gofi.assistant.mode'
/** Dev only: a JSON Turn[] in sessionStorage seeds the transcript (screenshots, demos). */
const SEED_KEY = 'gofi.assistant.seed'
type Mode = 'docked' | 'window'

const readMode = (): Mode => { try { return localStorage.getItem(MODE_KEY) === 'window' ? 'window' : 'docked' } catch { return 'docked' } }
const readSeed = (): Turn[] => {
  if (!import.meta.env.DEV) return []
  try { const seed = sessionStorage.getItem(SEED_KEY); return seed ? JSON.parse(seed) as Turn[] : [] } catch { return [] }
}

interface Decide { (target: { id: string; kind: 'proposal' | 'tool' } | Proposal, approve: boolean): void }

/** GoFiBot's chat beside the page (`children` is the page): docked in a
 * resizable split, or popped out into a floating window; both share this
 * component's state (turns, conversation id). Stays mounted while hidden
 * so the transcript, with any proposal or paused tool call still waiting
 * for a decision, survives closing; the conversation id also survives a
 * reload (sessionStorage). A stream is cancelled when the panel closes or
 * a new message is sent. */
export function AssistantDrawer({ open, onClose, children }: { open: boolean; onClose: () => void; children: ReactNode }) {
  const styles = useStyles2(getStyles)
  const [turns, setTurns] = useState<Turn[]>(readSeed)
  const [conversationId, setConversationId] = useState(loadConversationId)
  const conversationRef = useRef(conversationId)
  conversationRef.current = conversationId
  const [summary, setSummary] = useState<ChatDone | null>(null)
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [mode, setMode] = useState<Mode>(readMode)
  /** Proposal and tool call ids with a decision request in flight. */
  const [deciding, setDeciding] = useState<ReadonlySet<string>>(() => new Set())
  const [historyOpen, setHistoryOpen] = useState(false)
  const [history, setHistory] = useState<{ loading: boolean; error: string; items: ConversationSummary[] }>({ loading: false, error: '', items: [] })
  const [loadingConversation, setLoadingConversation] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const composerRef = useRef<HTMLTextAreaElement>(null)
  const stickToBottom = useRef(true)

  /** A message handed over by a page (ASSISTANT_OPEN_EVENT), sent once the panel is open. */
  const handoverRef = useRef('')
  const sendRef = useRef<(message: string) => Promise<void>>(async () => undefined)
  const sendingRef = useRef(sending)
  sendingRef.current = sending

  const stop = useCallback(() => { abortRef.current?.abort(); abortRef.current = null; setSending(false) }, [])
  // Closing the panel cancels an in-flight reply; opening focuses the composer and sends a handed-over message.
  useEffect(() => {
    if (!open) { stop(); return }
    composerRef.current?.focus()
    const message = handoverRef.current
    if (!message) return
    handoverRef.current = ''
    // A reply in progress keeps streaming; the message waits in the composer.
    if (sendingRef.current) setDraft(message); else void sendRef.current(message)
  }, [open, stop])
  useEffect(() => {
    const onOpen = (event: Event) => {
      const message = (event as CustomEvent<AssistantOpenDetail>).detail?.message?.trim() ?? ''
      if (!message) return
      // The chrome opens the panel on this same event; when it is already open, send now.
      if (open && !sendingRef.current) void sendRef.current(message)
      else if (open) setDraft(message)
      else handoverRef.current = message
    }
    window.addEventListener(ASSISTANT_OPEN_EVENT, onOpen)
    return () => window.removeEventListener(ASSISTANT_OPEN_EVENT, onOpen)
  }, [open])
  useEffect(() => () => abortRef.current?.abort(), [])
  // Follow the stream unless the reader scrolled up.
  useEffect(() => { const list = listRef.current; if (list && stickToBottom.current) list.scrollTop = list.scrollHeight }, [turns, open, historyOpen, mode])
  const onScroll = () => { const list = listRef.current; if (list) stickToBottom.current = list.scrollHeight - list.scrollTop - list.clientHeight < 48 }
  const adoptConversation = (id: string) => { setConversationId(id); conversationRef.current = id; saveConversationId(id) }

  async function send(message: string, retried = false) {
    const text = message.trim()
    if (!text) return
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller
    const ids = { user: nextTurnId(), assistant: nextTurnId() }
    setDraft(''); setSending(true); setHistoryOpen(false); stickToBottom.current = true
    setTurns(prev => startExchange(prev, text, ids))
    const startedWith = conversationRef.current
    try {
      const id = await chat({ conversationId: startedWith, message: text, pageContext: pageContextOf(window.location.hash) }, {
        // The id arrives with the first event, so a paused tool call can be decided mid-stream.
        onConversationId: adoptConversation,
        onText: delta => setTurns(prev => appendText(prev, delta)),
        onToolCall: call => setTurns(prev => addToolCall(prev, call)),
        onToolResult: result => setTurns(prev => addToolResult(prev, result)),
        onProposal: proposal => setTurns(prev => addProposal(prev, proposal)),
        onHighlight: highlight => setTurns(prev => addHighlight(prev, highlight)),
        onNavigate: href => { setTurns(prev => addNavigation(prev, href)); navigate(href) },
        onDone: done => { setSummary(done); setTurns(prev => finishTurn(prev, { stopReason: done.stopReason }, ids.assistant)) },
      }, controller.signal)
      if (id && id !== conversationRef.current) adoptConversation(id)
      setTurns(prev => finishTurn(prev, controller.signal.aborted ? { stopReason: 'cancelled' } : {}, ids.assistant))
    } catch (reason) {
      // The server forgot this conversation (restart, 12 h): start a fresh one with the same message.
      if (!retried && startedWith && reason instanceof AssistantError && reason.code === Code.NotFound) {
        adoptConversation('')
        setTurns(prev => prev.filter(turn => turn.id !== ids.user && turn.id !== ids.assistant))
        if (abortRef.current === controller) abortRef.current = null
        await send(text, true)
        return
      }
      // Shown once, on the turn (no separate alert).
      setTurns(prev => finishTurn(prev, { error: errorMessage(reason) }, ids.assistant))
    } finally {
      if (abortRef.current === controller) { abortRef.current = null; setSending(false) }
    }
  }

  sendRef.current = send

  // Approves (applies) or rejects a proposed change, or approves/denies a
  // paused tool call. The card keeps its state in the turns, so it survives
  // closing the panel while the request is in flight.
  const decide: Decide = async (target, approve) => {
    const kind = 'kind' in target ? target.kind : 'proposal'
    if (deciding.has(target.id)) return
    setDeciding(prev => new Set(prev).add(target.id))
    try {
      const outcome = await confirmAssistantAction(conversationRef.current, target.id, approve)
      setTurns(prev => kind === 'tool' ? decideToolCall(prev, target.id, approve) : decideProposal(prev, target.id, { approved: approve, output: outcome.output, isError: outcome.isError }))
    } catch (reason) {
      const expired = reason instanceof AssistantError && reason.code === Code.NotFound
      setTurns(prev => kind === 'tool'
        ? failToolCall(prev, target.id, expired ? t('gofi.assistant.drawer.tool-call-expired', 'This call is no longer waiting (it timed out or was decided elsewhere).') : errorMessage(reason))
        : failProposal(prev, target.id, expired ? { expired: true } : { message: errorMessage(reason) }))
    } finally {
      setDeciding(prev => { const next = new Set(prev); next.delete(target.id); return next })
    }
  }

  async function newConversation() {
    stop()
    const previous = conversationRef.current
    setTurns([]); setSummary(null); setHistoryOpen(false); adoptConversation('')
    try { await resetConversation(previous) } catch { /* the server forgets it on its own eventually */ }
    composerRef.current?.focus()
  }

  const loadHistory = () => {
    setHistory(current => ({ ...current, loading: true, error: '' }))
    listAssistantConversations()
      .then(items => setHistory({ loading: false, error: '', items }))
      .catch(reason => setHistory(current => ({ ...current, loading: false, error: errorMessage(reason) })))
  }
  const toggleHistory = () => { if (!historyOpen) loadHistory(); setHistoryOpen(value => !value) }

  /** Continues a stored conversation: its transcript becomes the turns. */
  async function openConversation(id: string) {
    if (loadingConversation) return
    setLoadingConversation(id)
    try {
      const transcript = await getAssistantConversation(id)
      stop()
      setTurns(turnsFromTranscript(transcript)); setSummary(null); adoptConversation(transcript.conversationId || id)
      setHistoryOpen(false); stickToBottom.current = true
      composerRef.current?.focus()
    } catch (reason) {
      const gone = reason instanceof AssistantError && reason.code === Code.NotFound
      setHistory(current => ({ ...current, error: gone ? t('gofi.assistant.drawer.history-gone', 'That conversation is no longer available; the controller keeps conversations for about 12 hours.') : errorMessage(reason) }))
      if (gone) { if (id === conversationRef.current) adoptConversation(''); loadHistory() }
    } finally { setLoadingConversation('') }
  }

  const onComposerKey = (event: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== 'Enter' || event.nativeEvent.isComposing) return
    // Enter, Cmd+Enter and Ctrl+Enter send; Shift+Enter adds a line.
    if (event.metaKey || event.ctrlKey || !event.shiftKey) { event.preventDefault(); if (!sending) void send(draft) }
  }
  const switchMode = (next: Mode) => { setMode(next); try { localStorage.setItem(MODE_KEY, next) } catch { /* storage unavailable */ } }

  const content = (drag?: { onPointerDown: (event: ReactPointerEvent<HTMLElement>) => void }) => <>
    <header className={cx(styles.header, drag && styles.headerDraggable)} onPointerDown={drag?.onPointerDown}>
      <GoFiMark className={styles.headerIcon} />
      <Text element="h2" variant="h5">{t('gofi.assistant.drawer.title', 'GoFiBot')}</Text>
      <span className={styles.headerSpacer} />
      <IconButton name="history" tooltip={t('gofi.assistant.drawer.history', 'History')} aria-pressed={historyOpen} onClick={toggleHistory} />
      <IconButton name="plus" tooltip={t('gofi.assistant.drawer.new-conversation', 'New conversation')} onClick={() => void newConversation()} />
      {mode === 'docked'
        ? <IconButton name="external-link-alt" tooltip={t('gofi.assistant.drawer.open-as-window', 'Open as window')} onClick={() => switchMode('window')} className={styles.hideNarrow} />
        : <IconButton name="columns" tooltip={t('gofi.assistant.drawer.dock', 'Dock to the side')} onClick={() => switchMode('docked')} />}
      <IconButton name="times" tooltip={t('gofi.common.close', 'Close')} onClick={onClose} />
    </header>
    {historyOpen
      ? <div className={styles.messages} role="list" aria-label={t('gofi.assistant.drawer.history', 'History')}>
        <Stack justifyContent="space-between" alignItems="center">
          <Text weight="medium">{t('gofi.assistant.drawer.history-title', 'Conversations')}</Text>
          <IconButton name="sync" tooltip={t('gofi.assistant.drawer.history-refresh', 'Refresh')} onClick={loadHistory} disabled={history.loading} />
        </Stack>
        {history.error && <Text color="error" variant="bodySmall">{history.error}</Text>}
        {history.loading && !history.items.length ? <Text color="secondary"><Icon name="spinner" /> {t('gofi.assistant.drawer.history-loading', 'Loading conversations…')}</Text>
          : !history.items.length && !history.error ? <Text color="secondary">{t('gofi.assistant.drawer.history-empty', 'No conversations yet. The controller keeps them for about 12 hours.')}</Text>
          : history.items.map(item => <button key={item.id} type="button" role="listitem" className={cx(styles.historyItem, item.id === conversationId && styles.historyItemCurrent)} disabled={!!loadingConversation} aria-current={item.id === conversationId ? 'true' : undefined} onClick={() => void openConversation(item.id)}>
            <span className={styles.historyTitle}>{item.title || t('gofi.assistant.drawer.history-untitled', 'Untitled conversation')}</span>
            <span className={styles.historyMeta}>
              <span>{formatRelativeTime(item.updatedUnixMs)}</span>
              <span>· {t('gofi.assistant.drawer.history-messages', '{{count}} messages', { count: item.messages })}</span>
              {item.pendingProposals > 0 && <Badge color="orange" text={t('gofi.assistant.drawer.history-pending', '{{count}} pending', { count: item.pendingProposals })} />}
              {loadingConversation === item.id && <Icon name="spinner" />}
            </span>
          </button>)}
      </div>
      : <div ref={listRef} className={styles.messages} onScroll={onScroll} role="log" aria-live="polite" aria-busy={sending}>
        {!turns.length && <div className={styles.intro}>
          <Text color="secondary">{t('gofi.assistant.drawer.intro', 'Ask about this GoFi: its flows, CEL expressions, and Python libraries. GoFiBot reads the configuration, drafts changes, and can open the page you need.')}</Text>
        </div>}
        {turns.map(turn => turn.kind === 'user'
          ? <div key={turn.id} className={cx(styles.turn, styles.userTurn)}><div className={styles.userBubble}>{turn.text}</div></div>
          : <AssistantTurnView key={turn.id} turn={turn} deciding={deciding} onDecide={decide} />)}
      </div>}
    <div className={styles.composer}>
      <TextArea ref={composerRef} rows={2} value={draft} onChange={event => setDraft(event.currentTarget.value)} onKeyDown={onComposerKey}
        placeholder={t('gofi.assistant.drawer.placeholder', 'Ask GoFiBot… Enter or Ctrl+Enter sends, Shift+Enter adds a line')} aria-label={t('gofi.assistant.drawer.message', 'Message')} className={styles.textarea} />
      <Stack justifyContent="space-between" alignItems="center" gap={1}>
        <Text variant="bodySmall" color="secondary">{summary
          ? t('gofi.assistant.drawer.summary', '{{input}} in · {{output}} out · {{steps}} tool steps', { input: formatNumber(summary.inputTokens), output: formatNumber(summary.outputTokens), steps: formatNumber(summary.toolSteps) })
          : t('gofi.assistant.drawer.context-hint', 'GoFiBot sees which page you are on.')}</Text>
        {sending
          ? <Button size="sm" variant="secondary" icon="times" onClick={stop}>{t('gofi.assistant.drawer.stop', 'Stop')}</Button>
          : <Button size="sm" icon="message" disabled={!draft.trim()} onClick={() => void send(draft)}>{t('gofi.assistant.drawer.send', 'Send')}</Button>}
      </Stack>
    </div>
  </>

  const docked = open && mode === 'docked'
  return <>
    <ResizableSplit side="right" flush minPanelWidth={280} storageKey={SHARE_KEY} defaultShare={0.35} main={children}
      panel={docked ? <aside className={styles.panel} aria-label={t('gofi.assistant.drawer.title', 'GoFiBot')}>{content()}</aside> : null} />
    {open && mode === 'window' && <FloatingWindow storageKey={WINDOW_KEY} ariaLabel={t('gofi.assistant.drawer.title', 'GoFiBot')}>{drag => <div className={styles.windowBody}>{content(drag)}</div>}</FloatingWindow>}
  </>
}

const stopReasonText = (reason: string | undefined): string | undefined => {
  switch (reason) {
    case 'max_tool_steps': return t('gofi.assistant.drawer.stop-max-tool-steps', 'Stopped at the tool step limit; ask it to continue.')
    case 'max_tokens': return t('gofi.assistant.drawer.stop-max-tokens', 'The reply reached the length limit.')
    case 'refusal': return t('gofi.assistant.drawer.stop-refusal', 'The model declined this request.')
    case 'cancelled': return t('gofi.assistant.drawer.stop-cancelled', 'Stopped.')
    default: return undefined
  }
}

/** One reply: the robot in the gutter (no name label), then tool rows,
 * highlight cards, text, proposals, and the outcome notes. */
function AssistantTurnView({ turn, deciding, onDecide }: { turn: AssistantTurn; deciding: ReadonlySet<string>; onDecide: Decide }) {
  const styles = useStyles2(getStyles)
  const note = stopReasonText(turn.stopReason)
  const empty = !turn.text && !turn.tools.length && !turn.proposals.length && !turn.highlights.length && !turn.opened.length
  return <div className={cx(styles.turn, styles.assistantRow)}>
    <span className={styles.gutter} aria-hidden><GoFiMark size={28} /></span>
    <div className={styles.assistantTurn}>
      {turn.tools.length > 0 && <div className={styles.tools}>{turn.tools.map(tool => <ToolRow key={tool.id} tool={tool} deciding={deciding.has(tool.id)} onDecide={onDecide} />)}</div>}
      {turn.opened.map(href => <a key={href} href={href} className={styles.chip} onClick={event => { event.preventDefault(); navigate(href) }}><Icon name="external-link-alt" size="sm" /> {t('gofi.assistant.drawer.opened', 'Opened {{href}}', { href })}</a>)}
      {turn.highlights.map(highlight => <Suspense key={`${highlight.flow}:${highlight.block}`} fallback={<Text color="secondary" variant="bodySmall"><Icon name="spinner" /> {highlight.flow} · {highlight.block}</Text>}><FlowHighlightCard highlight={highlight} /></Suspense>)}
      {turn.text ? <Markdown text={turn.text} /> : turn.streaming && empty ? <Text color="secondary"><Icon name="spinner" /> {t('gofi.assistant.drawer.thinking', 'Thinking…')}</Text> : null}
      {turn.streaming && turn.text && <span className={styles.cursor} aria-hidden />}
      {turn.proposals.map(proposal => <ProposalCard key={proposal.id} proposal={proposal} deciding={deciding.has(proposal.id)} onDecide={onDecide} />)}
      {turn.error && <div className={styles.turnError} role="alert"><Icon name="exclamation-triangle" /> <span>{turn.error}</span></div>}
      {note && <div className={styles.note}><Text color="secondary" variant="bodySmall">{note}</Text></div>}
    </div>
  </div>
}

/** A change the assistant proposed: what it would do, the exact input
 * (collapsed), and Approve/Reject while pending; afterwards the outcome.
 * Approving takes a deliberate click on the button: there is no keyboard
 * shortcut and the button is never focused automatically. */
function ProposalCard({ proposal, deciding, onDecide }: { proposal: Proposal; deciding: boolean; onDecide: Decide }) {
  const styles = useStyles2(getStyles)
  const [expanded, setExpanded] = useState(false)
  const input = proposalInput(proposal.name, proposal.inputJson)
  const codeLanguage = proposal.name === 'apply_flow' ? 'yaml' : proposal.name === 'apply_library' ? 'python' : proposal.name === 'apply_expression' ? 'cel' : 'plaintext'
  const output = truncateOutput(proposal.output ?? '')
  const pending = !proposal.decision
  const failed = proposal.decision === 'approved' && proposal.isError
  return <div className={cx(styles.proposal, failed && styles.toolError)} role="group" aria-label={t('gofi.assistant.proposal.title', 'Proposed change')}>
    <div className={styles.proposalHeader}>
      <Icon name="pen" className={styles.proposalIcon} />
      <div className={styles.proposalHeading}>
        <Text weight="medium">{t('gofi.assistant.proposal.title', 'Proposed change')}</Text>
        {proposal.summary && <Text color="secondary" variant="bodySmall">{proposal.summary}</Text>}
      </div>
      <Badge color="blue" text={proposal.name} className={styles.proposalTool} />
    </div>
    <button type="button" className={styles.toolHeader} onClick={() => setExpanded(value => !value)} aria-expanded={expanded}>
      <Icon name={expanded ? 'angle-down' : 'angle-right'} />
      <span className={styles.toolName}>{expanded ? t('gofi.assistant.proposal.hide-input', 'Hide input') : t('gofi.assistant.proposal.show-input', 'Show input')}</span>
    </button>
    {expanded && <div className={styles.proposalInput}>
      {input.rest && <CodeBlock source={input.rest} language="json" />}
      {input.code !== undefined && <CodeBlock source={input.code || ''} language={codeLanguage} label={codeLanguage} />}
    </div>}
    {pending && proposal.error && <Text color="error" variant="bodySmall">{proposal.error}</Text>}
    {pending
      ? <Stack gap={1} alignItems="center">
        <Button size="sm" variant="primary" icon={deciding ? 'spinner' : 'check'} disabled={deciding} onClick={() => onDecide(proposal, true)}>{t('gofi.assistant.proposal.approve', 'Approve')}</Button>
        <Button size="sm" variant="secondary" icon="times" disabled={deciding} onClick={() => onDecide(proposal, false)}>{t('gofi.assistant.proposal.reject', 'Reject')}</Button>
        {deciding && <Text color="secondary" variant="bodySmall">{t('gofi.assistant.proposal.deciding', 'Working…')}</Text>}
      </Stack>
      : proposal.decision === 'rejected' ? <Stack gap={1} alignItems="center"><Badge color="darkgrey" icon="times" text={t('gofi.assistant.proposal.rejected', 'Rejected')} /></Stack>
      : proposal.decision === 'expired' ? <Stack gap={1} alignItems="center"><Badge color="darkgrey" text={t('gofi.assistant.proposal.expired', 'No longer pending')} /><Text color="secondary" variant="bodySmall">{t('gofi.assistant.proposal.expired-hint', 'It was already decided, or the server forgot it.')}</Text></Stack>
      : <>
        <Stack gap={1} alignItems="center"><Badge color={failed ? 'red' : 'green'} icon={failed ? 'exclamation-triangle' : 'check'} text={failed ? t('gofi.assistant.proposal.apply-failed', 'Approved, but applying failed') : t('gofi.assistant.proposal.approved', 'Approved')} /></Stack>
        <CodeBlock source={output.text ? `${output.text}${output.cut > 0 ? `\n… ${t('gofi.assistant.drawer.tool-output-cut', '{{count}} more characters', { count: formatNumber(output.cut) })}` : ''}` : '—'} language={outputLanguage(output.text)} tone={failed ? 'error' : 'default'} />
      </>}
    {pending && <Text color="secondary" variant="bodySmall">{t('gofi.assistant.proposal.hint', 'Nothing is applied until you approve. Review the input first.')}</Text>}
  </div>
}

/** One tool call: collapsed to its name, a one-line summary of its input
 * and its state; expanded to the pretty input (code fields as code blocks)
 * and the output. A call waiting for approval shows Approve/Deny. */
function ToolRow({ tool, deciding, onDecide }: { tool: ToolActivity; deciding: boolean; onDecide: Decide }) {
  const styles = useStyles2(getStyles)
  const [expanded, setExpanded] = useState(false)
  const input = toolInputView(tool.inputJson)
  const output = truncateOutput(tool.output ?? '')
  const waiting = !!tool.needsApproval && !tool.done && !tool.decision
  const state = waiting ? t('gofi.assistant.drawer.tool-needs-approval', 'Needs approval')
    : !tool.done ? (tool.decision === 'denied' ? t('gofi.assistant.drawer.tool-denied', 'Denied') : t('gofi.assistant.drawer.tool-running', 'Running'))
    : tool.isError ? t('gofi.assistant.drawer.tool-failed', 'Failed') : t('gofi.assistant.drawer.tool-done', 'Done')
  const color = waiting ? 'orange' : !tool.done ? 'blue' : tool.isError ? 'red' : 'green'
  return <div className={cx(styles.tool, tool.isError && styles.toolError, waiting && styles.toolWaiting)}>
    <button type="button" className={styles.toolHeader} onClick={() => setExpanded(value => !value)} aria-expanded={expanded}>
      <Icon name={expanded ? 'angle-down' : 'angle-right'} />
      <Icon name={waiting ? 'pause' : !tool.done ? 'spinner' : tool.isError ? 'exclamation-triangle' : 'wrench'} className={styles.toolIcon} />
      <span className={styles.toolName}>{tool.name}</span>
      <span className={styles.toolSummary}>{summarizeInput(tool.inputJson)}</span>
      <Badge color={color} text={state} />
    </button>
    {waiting && <div className={styles.toolApproval}>
      <Text variant="bodySmall" color="secondary">{t('gofi.assistant.drawer.tool-approval-hint', 'The reply is paused until you decide; after two minutes the call is denied.')}</Text>
      {tool.error && <Text color="error" variant="bodySmall">{tool.error}</Text>}
      <Stack gap={1} alignItems="center">
        <Button size="sm" variant="primary" icon={deciding ? 'spinner' : 'check'} disabled={deciding} onClick={() => onDecide({ id: tool.id, kind: 'tool' }, true)}>{t('gofi.assistant.drawer.tool-approve', 'Approve')}</Button>
        <Button size="sm" variant="secondary" icon="times" disabled={deciding} onClick={() => onDecide({ id: tool.id, kind: 'tool' }, false)}>{t('gofi.assistant.drawer.tool-deny', 'Deny')}</Button>
      </Stack>
    </div>}
    {expanded && <div className={styles.toolBody}>
      <Text variant="bodySmall" color="secondary">{t('gofi.assistant.drawer.tool-input', 'Input')}</Text>
      {input.fields ? <CodeBlock source={input.fields} language="json" /> : !input.code.length && <CodeBlock source="{}" language="json" />}
      {input.code.map(field => <CodeBlock key={field.key} source={field.source} language={fieldLanguage(field.key)} label={field.key} />)}
      {tool.done && <>
        <Text variant="bodySmall" color="secondary">{tool.isError ? t('gofi.assistant.drawer.tool-error', 'Error') : t('gofi.assistant.drawer.tool-output', 'Output')}</Text>
        <CodeBlock source={output.text ? `${output.text}${output.cut > 0 ? `\n… ${t('gofi.assistant.drawer.tool-output-cut', '{{count}} more characters', { count: formatNumber(output.cut) })}` : ''}` : '—'} language={tool.isError ? 'plaintext' : outputLanguage(output.text)} tone={tool.isError ? 'error' : 'default'} />
      </>}
    </div>}
  </div>
}

const getStyles = (theme: GrafanaTheme2) => ({
  panel: css({
    // The split's pane takes its minimum from this; the panel never gets narrower than a readable transcript.
    flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, height: '100%',
    // Above the page: nothing the page scrolls (editor widgets, sticky columns) may paint over the chat.
    position: 'relative', zIndex: 1,
    background: theme.colors.background.primary, borderLeft: `1px solid ${theme.colors.border.weak}`,
    // Phones: cover the page instead of squeezing it.
    [theme.breakpoints.down('md')]: { position: 'absolute', top: 0, bottom: 0, right: 0, width: '100%', zIndex: theme.zIndex.navbarFixed + 1, boxShadow: theme.shadows.z3 },
  }),
  windowBody: css({ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0, minWidth: 0 }),
  header: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1), padding: theme.spacing(1, 1, 1, 2), borderBottom: `1px solid ${theme.colors.border.weak}`, h2: { margin: 0 } }),
  headerDraggable: css({ cursor: 'move', touchAction: 'none', userSelect: 'none' }),
  headerIcon: css({ flexShrink: 0, display: 'block' }),
  headerSpacer: css({ flex: 1 }),
  hideNarrow: css({ [theme.breakpoints.down('md')]: { display: 'none' } }),
  messages: css({ flex: 1, minHeight: 0, overflowY: 'auto', padding: theme.spacing(2), display: 'flex', flexDirection: 'column', gap: theme.spacing(2), overscrollBehavior: 'contain' }),
  intro: css({ padding: theme.spacing(1, 0) }),
  turn: css({ minWidth: 0, maxWidth: '100%' }),
  userTurn: css({ display: 'flex', justifyContent: 'flex-end' }),
  userBubble: css({
    maxWidth: '85%', padding: theme.spacing(1, 1.5), whiteSpace: 'pre-wrap', overflowWrap: 'anywhere',
    background: theme.colors.primary.transparent, border: `1px solid ${theme.colors.primary.border}`, borderRadius: theme.shape.radius.default,
  }),
  assistantRow: css({ display: 'grid', gridTemplateColumns: `${theme.spacing(3.5)} minmax(0, 1fr)`, columnGap: theme.spacing(1), alignItems: 'start' }),
  // The square GoFi mark, as in the top-left of the chrome: the tile is the badge.
  gutter: css({ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', width: theme.spacing(3.5), height: theme.spacing(3.5), marginTop: 0, svg: { display: 'block' } }),
  assistantTurn: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1), minWidth: 0 }),
  cursor: css({ display: 'inline-block', width: 8, height: '1em', marginLeft: 2, background: theme.colors.text.secondary, animation: 'gofi-assistant-blink 1s steps(2) infinite', '@keyframes gofi-assistant-blink': { to: { opacity: 0 } } }),
  note: css({ paddingTop: theme.spacing(0.5) }),
  turnError: css({ display: 'flex', alignItems: 'flex-start', gap: theme.spacing(1), padding: theme.spacing(1), color: theme.colors.error.text, background: theme.colors.error.transparent, border: `1px solid ${theme.colors.error.border}`, borderRadius: theme.shape.radius.default, overflowWrap: 'anywhere', fontSize: theme.typography.bodySmall.fontSize }),
  chip: css({
    alignSelf: 'flex-start', display: 'inline-flex', alignItems: 'center', gap: theme.spacing(0.5), padding: theme.spacing(0.25, 1), fontSize: theme.typography.bodySmall.fontSize,
    color: theme.colors.text.secondary, background: theme.colors.background.secondary, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.pill, textDecoration: 'none',
    '&:hover': { color: theme.colors.text.primary, borderColor: theme.colors.border.medium },
  }),
  tools: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5) }),
  tool: css({ border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.secondary }),
  toolError: css({ borderColor: theme.colors.error.border }),
  toolWaiting: css({ borderColor: theme.colors.warning.border }),
  toolApproval: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1), padding: theme.spacing(0.5, 1, 1), borderTop: `1px solid ${theme.colors.border.weak}` }),
  proposal: css({
    display: 'flex', flexDirection: 'column', gap: theme.spacing(1), padding: theme.spacing(1, 1.5, 1.5),
    border: `1px solid ${theme.colors.primary.border}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.secondary,
  }),
  proposalHeader: css({ display: 'flex', alignItems: 'flex-start', gap: theme.spacing(1) }),
  proposalIcon: css({ color: theme.colors.primary.text, marginTop: 2 }),
  proposalHeading: css({ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', overflowWrap: 'anywhere' }),
  proposalTool: css({ fontFamily: theme.typography.fontFamilyMonospace, flexShrink: 0 }),
  proposalInput: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5) }),
  toolHeader: css({
    display: 'flex', alignItems: 'center', gap: theme.spacing(1), width: '100%', padding: theme.spacing(0.5, 1), border: 'none', background: 'none', color: theme.colors.text.primary, cursor: 'pointer', textAlign: 'left',
    fontSize: theme.typography.bodySmall.fontSize, '&:hover': { background: theme.colors.action.hover }, '&:focus-visible': { outline: `2px solid ${theme.colors.primary.main}`, outlineOffset: -2 },
  }),
  toolIcon: css({ color: theme.colors.text.secondary, flexShrink: 0 }),
  toolName: css({ flexShrink: 0, maxWidth: '45%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: theme.typography.fontFamilyMonospace }),
  toolSummary: css({ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: theme.colors.text.secondary }),
  toolBody: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5), padding: theme.spacing(0.5, 1, 1), borderTop: `1px solid ${theme.colors.border.weak}` }),
  historyItem: css({
    display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5), width: '100%', padding: theme.spacing(1, 1.5), textAlign: 'left', cursor: 'pointer',
    color: theme.colors.text.primary, background: theme.colors.background.secondary, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default,
    '&:hover': { borderColor: theme.colors.border.medium, background: theme.colors.action.hover }, '&:disabled': { cursor: 'default', opacity: 0.7 },
    '&:focus-visible': { outline: `2px solid ${theme.colors.primary.main}`, outlineOffset: 1 },
  }),
  historyItemCurrent: css({ borderColor: theme.colors.primary.border, background: theme.colors.primary.transparent }),
  historyTitle: css({ fontWeight: theme.typography.fontWeightMedium, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
  historyMeta: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(0.5), flexWrap: 'wrap', fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary }),
  composer: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1), padding: theme.spacing(1.5, 2, 2), borderTop: `1px solid ${theme.colors.border.weak}` }),
  textarea: css({ resize: 'vertical', minHeight: theme.spacing(7) }),
})
