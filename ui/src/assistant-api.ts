import { createClient, ConnectError, Code } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import {
  AssistantService, type AssistantConfiguration as ProtoConfiguration, type AssistantProposal as ProtoProposal, type AssistantTool as ProtoTool,
  type AssistantToolCall as ProtoToolCall, type AssistantToolResult as ProtoToolResult,
} from './gen/gofi/v1/assistant_pb'
import { clampToolSteps, DEFAULT_MAX_TOOL_STEPS } from './components/assistant/helpers'
import { t, translateServerError } from './i18n'

const client = createClient(AssistantService, createConnectTransport({ baseUrl: window.location.origin, useBinaryFormat: true }))

export type ApiKeySource = 'stored' | 'environment' | 'none'
export type AssistantProvider = 'anthropic' | 'openai'
export type AssistantEffort = '' | 'low' | 'medium' | 'high'
/** "changes": only apply tools wait, as proposals decided after the reply.
 * "all": every tool call pauses the reply until the user approves or denies it. */
export type AssistantToolApproval = 'changes' | 'all'

/** GoFiBot's model endpoint settings (docs: Administration). */
export interface AssistantConfiguration {
  enabled: boolean
  /** Wire protocol of the endpoint: Anthropic Messages API or OpenAI-compatible Chat Completions. */
  provider: AssistantProvider
  /** Empty uses the Anthropic API; otherwise a gateway speaking the provider's protocol. */
  baseUrl: string
  model: string
  /** Write-only: never returned; empty on apply keeps the stored key. */
  apiKey: string
  /** Read-only. */
  hasApiKey: boolean
  /** Read-only. */
  apiKeySource: ApiKeySource
  /** Off: the assistant only reads, validates, and drafts. */
  allowChanges: boolean
  /** Python libraries may call the endpoint through gofi.llm (one switch for the deployment). */
  allowFunctions: boolean
  instructions: string
  /** 1-20. */
  maxToolSteps: number
  effort: AssistantEffort
  toolApproval: AssistantToolApproval
}
export interface AssistantConfigurationState {
  configuration: AssistantConfiguration
  /** Where it is stored: "file:<path>" or "kubernetes:<namespace>/<secret>". */
  backend: string
  /** Enabled, has a key, and the endpoint answered a cached probe. */
  ready: boolean
  /** The probed model when ready, otherwise why not. */
  readyMessage: string
  /** GOFI_ASSISTANT_BASE_URL pins the endpoint; the field cannot be changed here. */
  endpointLocked: boolean
  /** GOFI_ASSISTANT_API_KEY(_FILE) pins the key; the field cannot be changed here. */
  keyLocked: boolean
  /** GOFI_ASSISTANT_ALLOW_FUNCTIONS pins allowFunctions. */
  functionsLocked: boolean
}
export interface AssistantTool { name: string; description: string; mutating: boolean; enabled: boolean }
export interface ConnectionCheck { ok: boolean; model: string; message: string; latencyMs: number }
/** `needsApproval`: the reply is paused until `confirmAssistantAction(conversationId, id, approve)`; two minutes at most, then it is denied. */
export interface AssistantToolCall { id: string; name: string; inputJson: string; needsApproval: boolean }
export interface AssistantToolResult { id: string; name: string; output: string; isError: boolean }
/** A change the assistant wants to make; nothing is applied until `confirmAssistantAction` approves it. */
export interface AssistantProposal { id: string; name: string; inputJson: string; summary: string }
/** The tool's result (or the rejection note) as the model will see it. */
export interface ProposalDecision { output: string; isError: boolean }
/** One part of a flow the assistant points at while explaining; `block` is
 * source | frame | decode | parse | encode | stage:<id> | sink:<id or index>. */
export interface AssistantHighlight { flow: string; block: string; note: string }
export interface ConversationSummary { id: string; title: string; updatedUnixMs: number; messages: number; pendingProposals: number }
/** One transcript entry: a user message, an assistant reply with its tool calls, or the results of those calls. */
export interface TranscriptMessage { role: 'user' | 'assistant' | 'tool'; text: string; calls: AssistantToolCall[]; results: AssistantToolResult[] }
export interface ConversationTranscript { conversationId: string; messages: TranscriptMessage[]; pendingProposals: AssistantProposal[] }
export interface ChatDone { stopReason: string; inputTokens: number; outputTokens: number; toolSteps: number }
export interface ChatInput { conversationId: string; message: string; pageContext: string }
export interface ChatHandlers {
  onText?: (delta: string) => void
  onToolCall?: (call: AssistantToolCall) => void
  onToolResult?: (result: AssistantToolResult) => void
  onProposal?: (proposal: AssistantProposal) => void
  onHighlight?: (highlight: AssistantHighlight) => void
  onNavigate?: (href: string) => void
  /** The conversation id, as soon as the first event carries it (before the stream ends). */
  onConversationId?: (conversationId: string) => void
  onDone?: (done: ChatDone) => void
}

export const emptyConfiguration = (): AssistantConfiguration => ({
  enabled: false, provider: 'anthropic', baseUrl: '', model: '', apiKey: '', hasApiKey: false, apiKeySource: 'none', allowChanges: false, allowFunctions: false, instructions: '', maxToolSteps: DEFAULT_MAX_TOOL_STEPS, effort: '', toolApproval: 'changes',
})

/** A Connect failure with a user-facing message; `code` keeps the status for callers that branch on it. */
export class AssistantError extends Error {
  code: Code
  constructor(message: string, code: Code) { super(message); this.name = 'AssistantError'; this.code = code }
}

export const isUnimplemented = (error: unknown): boolean => error instanceof AssistantError ? error.code === Code.Unimplemented : ConnectError.from(error).code === Code.Unimplemented
/** The request was cancelled by its AbortSignal, not by the server. */
export const isAborted = (error: unknown): boolean =>
  (error instanceof DOMException && error.name === 'AbortError') || (error instanceof AssistantError ? error.code === Code.Canceled : ConnectError.from(error).code === Code.Canceled)
/** The message of any error, readable: assistant errors as mapped, other Connect errors by status. */
export function errorMessage(error: unknown): string {
  if (error instanceof AssistantError) return error.message
  if (error instanceof ConnectError) return assistantErrorMessage(error)
  return error instanceof Error ? error.message : String(error)
}

/** Readable text for a failed assistant RPC, by status code. */
export function assistantErrorMessage(error: unknown): string {
  const connect = ConnectError.from(error)
  const raw = connect.rawMessage.trim()
  switch (connect.code) {
    case Code.Unimplemented: return t('gofi.lib.assistant-api.unimplemented', 'This GoFi controller has no assistant.')
    case Code.FailedPrecondition: return raw || t('gofi.lib.assistant-api.not-configured', 'The assistant is disabled or has no API key. Configure it under Administration.')
    case Code.Unavailable: return raw ? t('gofi.lib.assistant-api.unavailable', 'The assistant endpoint did not answer: {{message}}', { message: raw }) : t('gofi.lib.assistant-api.unavailable-plain', 'The assistant endpoint did not answer.')
    case Code.Canceled: return t('gofi.lib.assistant-api.cancelled', 'The request was cancelled.')
    case Code.InvalidArgument: return raw || t('gofi.lib.assistant-api.invalid', 'The request was rejected as invalid.')
    case Code.ResourceExhausted: return raw ? t('gofi.lib.assistant-api.busy', 'GoFiBot is busy: {{message}}. Try again in a moment.', { message: raw }) : t('gofi.lib.assistant-api.busy-plain', 'GoFiBot is busy with another reply or other users. Try again in a moment.')
    case Code.DeadlineExceeded: return raw ? t('gofi.lib.assistant-api.timeout', 'The reply ran out of time: {{message}}. Ask a narrower question or raise the limit.', { message: raw }) : t('gofi.lib.assistant-api.timeout-plain', 'The reply ran out of time. Ask a narrower question or send it again.')
    default: return translateServerError(connect.code, { message: raw })
  }
}
const wrap = (error: unknown): AssistantError => error instanceof AssistantError ? error : new AssistantError(assistantErrorMessage(error), ConnectError.from(error).code)

function configurationFromProto(proto: ProtoConfiguration | undefined): AssistantConfiguration {
  if (!proto) return emptyConfiguration()
  const source = proto.apiKeySource === 'stored' || proto.apiKeySource === 'environment' ? proto.apiKeySource : 'none'
  const effort = proto.effort === 'low' || proto.effort === 'medium' || proto.effort === 'high' ? proto.effort : ''
  return {
    enabled: proto.enabled, provider: proto.provider === 'openai' ? 'openai' : 'anthropic', baseUrl: proto.baseUrl, model: proto.model, apiKey: '', hasApiKey: proto.hasApiKey, apiKeySource: source,
    allowChanges: proto.allowChanges, allowFunctions: proto.allowFunctions, instructions: proto.instructions, maxToolSteps: clampToolSteps(proto.maxToolSteps), effort,
    toolApproval: proto.toolApproval === 'all' ? 'all' : 'changes',
  }
}
/** The wire shape; read-only fields are sent as-is and ignored by the server. */
const configurationToProto = (configuration: AssistantConfiguration) => ({
  enabled: configuration.enabled, provider: configuration.provider, baseUrl: configuration.baseUrl.trim(), model: configuration.model.trim(), apiKey: configuration.apiKey,
  hasApiKey: configuration.hasApiKey, apiKeySource: configuration.apiKeySource, allowChanges: configuration.allowChanges, allowFunctions: configuration.allowFunctions,
  instructions: configuration.instructions, maxToolSteps: clampToolSteps(configuration.maxToolSteps), effort: configuration.effort, toolApproval: configuration.toolApproval,
})
const toolFromProto = (tool: ProtoTool): AssistantTool => ({ name: tool.name, description: tool.description, mutating: tool.mutating, enabled: tool.enabled })
const toolCallFromProto = (call: ProtoToolCall): AssistantToolCall => ({ id: call.id, name: call.name, inputJson: call.inputJson, needsApproval: call.needsApproval })
const toolResultFromProto = (result: ProtoToolResult): AssistantToolResult => ({ id: result.id, name: result.name, output: result.output, isError: result.isError })
const proposalFromProto = (proposal: ProtoProposal): AssistantProposal => ({ id: proposal.id, name: proposal.name, inputJson: proposal.inputJson, summary: proposal.summary })

export async function getAssistantConfiguration(): Promise<AssistantConfigurationState> {
  try {
    const response = await client.getAssistantConfiguration({})
    return {
      configuration: configurationFromProto(response.configuration), backend: response.backend, ready: response.ready, readyMessage: response.readyMessage,
      endpointLocked: response.endpointLocked, keyLocked: response.keyLocked, functionsLocked: response.functionsLocked,
    }
  } catch (error) { throw wrap(error) }
}
/** Saves the configuration; the returned copy never carries the key.
 * `clearApiKey` forgets the stored key (an environment key, if any, stays). */
export async function applyAssistantConfiguration(configuration: AssistantConfiguration, options: { clearApiKey?: boolean } = {}): Promise<AssistantConfiguration> {
  try {
    const response = await client.applyAssistantConfiguration({ configuration: configurationToProto(options.clearApiKey ? { ...configuration, apiKey: '' } : configuration), clearApiKey: !!options.clearApiKey })
    return configurationFromProto(response.configuration)
  } catch (error) { throw wrap(error) }
}
/** Probes the endpoint with unsaved values (an empty key uses the stored one); without values, the saved configuration. */
export async function checkAssistantConnection(configuration?: AssistantConfiguration): Promise<ConnectionCheck> {
  try {
    const response = await client.checkAssistantConnection({ configuration: configuration ? configurationToProto(configuration) : undefined })
    return { ok: response.ok, model: response.model, message: response.message, latencyMs: Number(response.latencyMs) }
  } catch (error) { throw wrap(error) }
}
export async function listAssistantTools(): Promise<AssistantTool[]> {
  try { return (await client.listAssistantTools({})).tools.map(toolFromProto) } catch (error) { throw wrap(error) }
}
export async function resetConversation(conversationId: string): Promise<void> {
  if (!conversationId) return
  try { await client.resetConversation({ conversationId }) } catch (error) { throw wrap(error) }
}
/** Conversations the controller still remembers (about 12 hours), newest first. */
export async function listAssistantConversations(): Promise<ConversationSummary[]> {
  try {
    const response = await client.listAssistantConversations({})
    return response.conversations
      .map(summary => ({ id: summary.id, title: summary.title, updatedUnixMs: Number(summary.updatedUnixMs), messages: summary.messages, pendingProposals: summary.pendingProposals }))
      .sort((a, b) => b.updatedUnixMs - a.updatedUnixMs)
  } catch (error) { throw wrap(error) }
}
/** The transcript of one conversation; rejects with NotFound when the controller forgot it. */
export async function getAssistantConversation(conversationId: string): Promise<ConversationTranscript> {
  try {
    const response = await client.getAssistantConversation({ conversationId })
    return {
      conversationId: response.conversationId,
      messages: response.messages.map(message => ({
        role: message.role === 'user' || message.role === 'tool' ? message.role : 'assistant', text: message.text,
        calls: message.calls.map(toolCallFromProto), results: message.results.map(toolResultFromProto),
      })),
      pendingProposals: response.pendingProposals.map(proposalFromProto),
    }
  } catch (error) { throw wrap(error) }
}
/** Approves (applies) or rejects a proposed change, or approves/denies a
 * paused tool call (`proposalId` is then the call's id). Rejects with
 * NotFound when the proposal is unknown or already decided, and
 * FailedPrecondition when changes were disabled meanwhile. A proposal's
 * decision reaches the model with the user's next message; a paused call
 * continues the reply at once. */
export async function confirmAssistantAction(conversationId: string, proposalId: string, approve: boolean): Promise<ProposalDecision> {
  try {
    const response = await client.confirmAssistantAction({ conversationId, proposalId, approve })
    return { output: response.output, isError: response.isError }
  } catch (error) { throw wrap(error) }
}

/** Sends one message and streams the reply into the handlers. Resolves with
 * the conversation id (new when the request had none), also when aborted
 * through `signal`; rejects with an AssistantError otherwise. */
export async function chat(input: ChatInput, handlers: ChatHandlers, signal?: AbortSignal): Promise<string> {
  let conversationId = input.conversationId
  try {
    for await (const event of client.chat({ conversationId: input.conversationId, message: input.message, pageContext: input.pageContext }, { signal })) {
      if (signal?.aborted) break
      if (event.conversationId && event.conversationId !== conversationId) { conversationId = event.conversationId; handlers.onConversationId?.(conversationId) }
      const { event: body } = event
      switch (body.case) {
        case 'textDelta': handlers.onText?.(body.value); break
        case 'toolCall': handlers.onToolCall?.(toolCallFromProto(body.value)); break
        case 'toolResult': handlers.onToolResult?.(toolResultFromProto(body.value)); break
        case 'proposal': handlers.onProposal?.(proposalFromProto(body.value)); break
        case 'highlight': handlers.onHighlight?.({ flow: body.value.flow, block: body.value.block, note: body.value.note }); break
        case 'navigate': handlers.onNavigate?.(body.value.href); break
        case 'done': handlers.onDone?.({ stopReason: body.value.stopReason, inputTokens: Number(body.value.inputTokens), outputTokens: Number(body.value.outputTokens), toolSteps: body.value.toolSteps }); break
      }
    }
  } catch (error) {
    if (signal?.aborted || isAborted(error)) return conversationId
    throw wrap(error)
  }
  return conversationId
}
