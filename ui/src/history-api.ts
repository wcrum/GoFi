import { createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import { FlowHistoryService, type FlowRevision as ProtoRevision } from './gen/gofi/v1/history_pb'
import { flowFromProto } from './rpc-model'
import type { FlowDefinition } from './types'
import type { RevisionSource } from './lib/revisions'
import { t } from './i18n'

const client = createClient(FlowHistoryService, createConnectTransport({ baseUrl: window.location.origin, useBinaryFormat: true }))

/** One immutable version of a flow (docs/versioning.md). */
export interface FlowRevision {
  flow: string
  revision: number
  createdAt: Date
  author: string
  message: string
  source: RevisionSource
  hash: string
  /** A tombstone: the flow was deleted at this revision. */
  deleted: boolean
  /** Only on getFlowRevision. */
  definition?: FlowDefinition
}
export interface RevisionList { revisions: FlowRevision[]; retentionLimit: number; backend: string }

function fromProto(rev: ProtoRevision): FlowRevision {
  return {
    flow: rev.flow, revision: Number(rev.revision), createdAt: rev.createdAt ? timestampDate(rev.createdAt) : new Date(0), author: rev.author || 'unknown',
    message: rev.message, source: (rev.source || 'portal') as RevisionSource, hash: rev.hash, deleted: rev.deleted,
    definition: rev.definition ? flowFromProto(rev.definition) : undefined,
  }
}

/** Revision summaries, newest first; limit 0 lists every retained revision. */
export async function listFlowRevisions(flow: string, limit = 0): Promise<RevisionList> {
  const response = await client.listFlowRevisions({ flow, limit })
  return { revisions: response.revisions.map(fromProto), retentionLimit: response.retentionLimit, backend: response.backend }
}
export async function getFlowRevision(flow: string, revision: number): Promise<FlowRevision> {
  const { revision: rev } = await client.getFlowRevision({ flow, revision: BigInt(revision) })
  if (!rev) throw new Error(t('gofi.lib.history-api.no-revision-returned', 'GoFi returned no revision'))
  return fromProto(rev)
}
/** Saves revision's definition as the flow (validation applies). created is
 * false when the flow already matched it. */
export async function rollbackFlow(flow: string, revision: number, message = ''): Promise<{ revision?: FlowRevision; created: boolean }> {
  const response = await client.rollbackFlow({ flow, revision: BigInt(revision), message })
  return { revision: response.revision ? fromProto(response.revision) : undefined, created: response.created }
}
