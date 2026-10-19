// The chat transcript as pure state: user turns, assistant turns that fill
// in as the stream arrives, the tool activity inside an assistant turn, the
// changes the assistant proposed (applied only once the user approves), and
// the flow blocks it pointed at. Every function returns a new array so React
// re-renders on each delta.

export interface ToolActivity {
  id: string
  name: string
  inputJson: string
  output?: string
  isError?: boolean
  /** The result arrived. */
  done: boolean
  /** The reply is paused until the user approves or denies this call. */
  needsApproval?: boolean
  decision?: 'approved' | 'denied'
  /** The last decision attempt failed; the call is still waiting. */
  error?: string
}
/** A proposed change. Pending until `decision` is set; the server applies
 * it only on approval, and the outcome arrives with the decision. */
export interface Proposal {
  id: string
  name: string
  inputJson: string
  summary: string
  /** "expired": the server no longer knows it (already decided elsewhere, or forgotten). */
  decision?: 'approved' | 'rejected' | 'expired'
  /** The tool's result (or the rejection note) as the model will see it. */
  output?: string
  isError?: boolean
  /** The last decision attempt failed; the proposal is still pending. */
  error?: string
}
/** One flow block the assistant pointed at (drawn as a pipeline card). */
export interface Highlight { flow: string; block: string; note: string }
export interface UserTurn { kind: 'user'; id: string; text: string }
export interface AssistantTurn {
  kind: 'assistant'
  id: string
  text: string
  tools: ToolActivity[]
  proposals: Proposal[]
  highlights: Highlight[]
  /** Pages the assistant opened (hrefs), shown as chips. */
  opened: string[]
  /** Still receiving events. */
  streaming: boolean
  /** The stream failed after this turn started; the text so far is kept. */
  error?: string
  /** "end_turn", "max_tokens", "max_tool_steps", "refusal". */
  stopReason?: string
}
export type Turn = UserTurn | AssistantTurn

export interface ChatSummary { inputTokens: number; outputTokens: number; toolSteps: number }

const CONVERSATION_KEY = 'gofi.assistant.conversation'

/** Tools whose only effect is in the portal: they render as a highlight card
 * or an "opened" chip (from the events that follow), never as tool rows. */
export const PORTAL_TOOLS: ReadonlySet<string> = new Set(['point_at_flow_block', 'open_page'])
export const isPortalTool = (name: string): boolean => PORTAL_TOOLS.has(name)

let counter = 0
export const nextTurnId = () => `turn-${Date.now().toString(36)}-${++counter}`

const emptyAssistant = (id: string, streaming: boolean): AssistantTurn => ({ kind: 'assistant', id, text: '', tools: [], proposals: [], highlights: [], opened: [], streaming })

/** Adds the user's message and an empty, streaming assistant turn. */
export function startExchange(turns: Turn[], message: string, ids: { user: string; assistant: string }): Turn[] {
  return [...turns, { kind: 'user', id: ids.user, text: message }, emptyAssistant(ids.assistant, true)]
}

function updateLastAssistant(turns: Turn[], update: (turn: AssistantTurn) => AssistantTurn): Turn[] {
  for (let i = turns.length - 1; i >= 0; i--) {
    const turn = turns[i]
    if (turn.kind === 'assistant') return [...turns.slice(0, i), update(turn), ...turns.slice(i + 1)]
  }
  return turns
}

export const appendText = (turns: Turn[], delta: string): Turn[] => delta ? updateLastAssistant(turns, turn => ({ ...turn, text: turn.text + delta })) : turns

/** Adds a tool call row; portal tools (highlight, open page) are not rows. */
export function addToolCall(turns: Turn[], call: { id: string; name: string; inputJson: string; needsApproval?: boolean }): Turn[] {
  if (isPortalTool(call.name)) return turns
  return updateLastAssistant(turns, turn => ({ ...turn, tools: [...turn.tools, { id: call.id, name: call.name, inputJson: call.inputJson, done: false, ...call.needsApproval ? { needsApproval: true } : {} }] }))
}

/** Fills in the matching call's result; an unknown id becomes its own row. */
export function addToolResult(turns: Turn[], result: { id: string; name: string; output: string; isError: boolean }): Turn[] {
  if (isPortalTool(result.name)) return turns
  return updateLastAssistant(turns, turn => {
    const index = turn.tools.findIndex(tool => tool.id === result.id)
    const row: ToolActivity = index >= 0
      ? { ...turn.tools[index], output: result.output, isError: result.isError, done: true }
      : { id: result.id, name: result.name, inputJson: '', output: result.output, isError: result.isError, done: true }
    const tools = index >= 0 ? turn.tools.map((tool, i) => i === index ? row : tool) : [...turn.tools, row]
    return { ...turn, tools }
  })
}

/** Updates the tool call `id` wherever it is; unknown ids leave the turns untouched. */
function updateToolCall(turns: Turn[], id: string, update: (tool: ToolActivity) => ToolActivity): Turn[] {
  let changed = false
  const next = turns.map(turn => {
    if (turn.kind !== 'assistant' || !turn.tools.some(tool => tool.id === id)) return turn
    changed = true
    return { ...turn, tools: turn.tools.map(tool => tool.id === id ? update(tool) : tool) }
  })
  return changed ? next : turns
}

/** Records the user's decision on a paused tool call; the result still
 * arrives through the stream (a denied call's result says so). */
export const decideToolCall = (turns: Turn[], id: string, approved: boolean): Turn[] =>
  updateToolCall(turns, id, ({ error: _error, ...tool }) => ({ ...tool, decision: approved ? 'approved' : 'denied' }))

/** A decision on a paused call could not be sent; the call keeps waiting. */
export const failToolCall = (turns: Turn[], id: string, message: string): Turn[] =>
  updateToolCall(turns, id, tool => ({ ...tool, error: message }))

/** Tool calls still waiting for the user's approval. */
export const pendingToolCalls = (turns: Turn[]): ToolActivity[] =>
  turns.flatMap(turn => turn.kind === 'assistant' ? turn.tools.filter(tool => tool.needsApproval && !tool.done && !tool.decision) : [])

/** Adds a flow block the assistant pointed at (one card per block). */
export const addHighlight = (turns: Turn[], highlight: Highlight): Turn[] =>
  updateLastAssistant(turns, turn => turn.highlights.some(h => h.flow === highlight.flow && h.block === highlight.block)
    ? { ...turn, highlights: turn.highlights.map(h => h.flow === highlight.flow && h.block === highlight.block ? { ...highlight } : h) }
    : { ...turn, highlights: [...turn.highlights, { ...highlight }] })

/** Records a page the assistant opened (shown as a chip). */
export const addNavigation = (turns: Turn[], href: string): Turn[] =>
  href ? updateLastAssistant(turns, turn => turn.opened.includes(href) ? turn : { ...turn, opened: [...turn.opened, href] }) : turns

/** Adds a proposed change to the current assistant turn. */
export const addProposal = (turns: Turn[], proposal: { id: string; name: string; inputJson: string; summary: string }): Turn[] =>
  updateLastAssistant(turns, turn => ({ ...turn, proposals: [...turn.proposals, { id: proposal.id, name: proposal.name, inputJson: proposal.inputJson, summary: proposal.summary }] }))

/** Updates the proposal `id` wherever it is; unknown ids leave the turns untouched. */
function updateProposal(turns: Turn[], id: string, update: (proposal: Proposal) => Proposal): Turn[] {
  let changed = false
  const next = turns.map(turn => {
    if (turn.kind !== 'assistant' || !turn.proposals.some(proposal => proposal.id === id)) return turn
    changed = true
    return { ...turn, proposals: turn.proposals.map(proposal => proposal.id === id ? update(proposal) : proposal) }
  })
  return changed ? next : turns
}

/** Records the user's decision and, for an approval, what applying it produced. */
export const decideProposal = (turns: Turn[], id: string, outcome: { approved: boolean; output: string; isError: boolean }): Turn[] =>
  updateProposal(turns, id, ({ error: _error, ...proposal }) => ({ ...proposal, decision: outcome.approved ? 'approved' : 'rejected', output: outcome.output, isError: outcome.isError }))

/** A decision could not be made: `expired` when the server no longer has
 * the proposal (it stops being pending), otherwise the message is shown and
 * the proposal stays pending. */
export function failProposal(turns: Turn[], id: string, failure: { expired: true } | { expired?: false; message: string }): Turn[] {
  return updateProposal(turns, id, ({ error: _error, ...proposal }) => failure.expired ? { ...proposal, decision: 'expired' } : { ...proposal, error: failure.message })
}

/** Proposals the user has not decided yet. */
export const pendingProposals = (turns: Turn[]): Proposal[] =>
  turns.flatMap(turn => turn.kind === 'assistant' ? turn.proposals.filter(proposal => !proposal.decision) : [])

/** Ends the assistant turn `id` (the last one when omitted): streaming
 * stops and pending tools settle. A stale stream ending after a newer
 * exchange started therefore never touches the newer turn. */
export function finishTurn(turns: Turn[], outcome: { stopReason?: string; error?: string } = {}, id?: string): Turn[] {
  const finish = (turn: AssistantTurn): AssistantTurn => ({ ...turn, streaming: false, tools: turn.tools.map(tool => tool.done ? tool : { ...tool, done: true }), ...outcome.stopReason ? { stopReason: outcome.stopReason } : {}, ...outcome.error ? { error: outcome.error } : {} })
  if (id === undefined) return updateLastAssistant(turns, finish)
  return turns.map(turn => turn.kind === 'assistant' && turn.id === id ? finish(turn) : turn)
}

export interface TranscriptMessageLike {
  role: 'user' | 'assistant' | 'tool'
  text: string
  calls: Array<{ id: string; name: string; inputJson: string; needsApproval?: boolean }>
  results: Array<{ id: string; name: string; output: string; isError: boolean }>
}
export interface TranscriptLike { messages: TranscriptMessageLike[]; pendingProposals: Array<{ id: string; name: string; inputJson: string; summary: string }> }

/** What a portal tool call in a stored transcript stands for. */
function portalEffect(call: { name: string; inputJson: string }, result: { output: string } | undefined): { highlight?: Highlight; opened?: string } {
  if (call.name === 'point_at_flow_block') {
    try {
      const input = JSON.parse(call.inputJson) as Record<string, unknown>
      const flow = typeof input.flow === 'string' ? input.flow : ''
      const block = typeof input.block === 'string' ? input.block : ''
      if (flow && block) return { highlight: { flow, block, note: typeof input.note === 'string' ? input.note : '' } }
    } catch { /* not JSON: nothing to draw */ }
    return {}
  }
  if (call.name === 'open_page') {
    const href = /(#\/\S*)/.exec(result?.output ?? '')?.[1]
    return href ? { opened: href } : {}
  }
  return {}
}

/** Rebuilds the turns of a stored conversation: user text; assistant text
 * with its tool calls; tool results merged into the matching call rows;
 * portal tool calls as highlight cards and "opened" chips; pending proposals
 * as pending cards on the last reply. Nothing is streaming; calls without a
 * result are shown settled. */
export function turnsFromTranscript(transcript: TranscriptLike, nextId: () => string = nextTurnId): Turn[] {
  const turns: Turn[] = []
  const results = new Map<string, { id: string; name: string; output: string; isError: boolean }>()
  for (const message of transcript.messages) for (const result of message.results) results.set(result.id, result)
  const proposalIds = new Set(transcript.pendingProposals.map(proposal => proposal.id))
  const lastAssistant = (): AssistantTurn => {
    const last = turns[turns.length - 1]
    if (last?.kind === 'assistant') return last
    const turn = emptyAssistant(nextId(), false)
    turns.push(turn)
    return turn
  }
  for (const message of transcript.messages) {
    if (message.role === 'user') { if (message.text) turns.push({ kind: 'user', id: nextId(), text: message.text }); continue }
    if (message.role === 'tool') continue // results are merged into the call rows below
    const turn = emptyAssistant(nextId(), false)
    turn.text = message.text
    for (const call of message.calls) {
      const result = results.get(call.id)
      if (isPortalTool(call.name)) {
        const effect = portalEffect(call, result)
        if (effect.highlight) turn.highlights.push(effect.highlight)
        if (effect.opened) turn.opened.push(effect.opened)
        continue
      }
      if (proposalIds.has(call.id)) continue // the pending card stands for it
      turn.tools.push(result
        ? { id: call.id, name: call.name, inputJson: call.inputJson, output: result.output, isError: result.isError, done: true }
        : { id: call.id, name: call.name, inputJson: call.inputJson, done: true })
    }
    turns.push(turn)
  }
  if (transcript.pendingProposals.length) {
    const turn = lastAssistant()
    turn.proposals = [...turn.proposals, ...transcript.pendingProposals.map(proposal => ({ id: proposal.id, name: proposal.name, inputJson: proposal.inputJson, summary: proposal.summary }))]
  }
  return turns
}

/** The conversation id survives a page reload within the tab. */
export function loadConversationId(storage: Pick<Storage, 'getItem'> | undefined = typeof sessionStorage === 'undefined' ? undefined : sessionStorage): string {
  try { return storage?.getItem(CONVERSATION_KEY) ?? '' } catch { return '' }
}
export function saveConversationId(id: string, storage: Pick<Storage, 'setItem' | 'removeItem'> | undefined = typeof sessionStorage === 'undefined' ? undefined : sessionStorage): void {
  try { if (id) storage?.setItem(CONVERSATION_KEY, id); else storage?.removeItem(CONVERSATION_KEY) } catch { /* storage unavailable */ }
}
