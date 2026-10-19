// The flow the tutorial loads into the New flow page: TCP listen, one JSON
// object per line, every message to stdout. On Kubernetes it asks for a
// NodePort (assigned by the cluster) so the ingress step has a port to show;
// other drivers reject Service exposure and bind the port on the host.

import type { FlowDefinition, TestCase } from '../../types'
import { SAMPLE_FRAME } from './commands.ts'

export const TUTORIAL_FLOW_NAME = 'tutorial-events'
export const TUTORIAL_FLOW_PORT = 9000

export function tutorialExampleFlow(driverKind: string | undefined, testCases: TestCase[] = []): FlowDefinition {
  const service = driverKind === 'kubernetes' ? { service: { type: 'NodePort' as const } } : {}
  return {
    name: TUTORIAL_FLOW_NAME,
    source: { listen: { network: 'tcp', port: TUTORIAL_FLOW_PORT, ...service } },
    framing: { mode: 'line' },
    parser: { format: 'json', errorPolicy: 'continue' },
    stages: [],
    sinks: [{ type: 'stdout' }],
    testCases,
  }
}

/** A test case for Mock mode: the same frame the ingress step sends. */
export const tutorialTestCase = (name: string): TestCase => ({ id: 'tutorial-sample', name, raw: SAMPLE_FRAME })
