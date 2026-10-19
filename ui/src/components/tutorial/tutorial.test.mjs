import { test } from 'node:test'
import assert from 'node:assert/strict'
import { STEP_COUNT, TUTORIAL_STEP_IDS, clampStep, docsUrl, isFirstStep, isLastStep, onStepPage, progress, stepHref, stepId, stepNumber } from './model.ts'
import { SAMPLE_FRAME, defaultHost, ingressKind, portForwardCommand, resolveIngress, shellQuote, testCommand } from './commands.ts'
import { TUTORIAL_STORAGE_KEY, clearTutorialState, readTutorialState, resumeStep, shouldAutoOpen, writeTutorialState } from './storage.ts'
import { TUTORIAL_FLOW_NAME, TUTORIAL_FLOW_PORT, tutorialExampleFlow, tutorialTestCase } from './example.ts'

// --- step model ---

test('six ordered steps, clamped to the range', () => {
  assert.equal(STEP_COUNT, 6)
  assert.deepEqual([...TUTORIAL_STEP_IDS], ['welcome', 'navigation', 'first-flow', 'ingress', 'monitoring', 'done'])
  assert.equal(clampStep(0), 1)
  assert.equal(clampStep(7), 6)
  assert.equal(clampStep(3.5), 1)
  assert.equal(clampStep(NaN), 1)
  assert.equal(stepId(1), 'welcome')
  assert.equal(stepId(99), 'done')
  assert.equal(stepNumber('ingress'), 4)
  assert.ok(isFirstStep(1) && !isFirstStep(2))
  assert.ok(isLastStep(6) && !isLastStep(5))
})

test('progress counts steps and rounds the percentage', () => {
  assert.deepEqual(progress(1), { step: 1, total: 6, percent: 17 })
  assert.deepEqual(progress(6), { step: 6, total: 6, percent: 100 })
  assert.deepEqual(progress(-4), { step: 1, total: 6, percent: 17 })
})

test('each step opens its real page; flow-aware steps use the saved flow', () => {
  assert.equal(stepHref('welcome'), '#/')
  assert.equal(stepHref('navigation'), null)
  assert.equal(stepHref('first-flow'), '#/flows/new')
  assert.equal(stepHref('ingress'), '#/flows/new')
  assert.equal(stepHref('ingress', { flowName: 'my flow' }), '#/flows/my%20flow/edit')
  assert.equal(stepHref('monitoring'), '#/explore')
  assert.equal(stepHref('monitoring', { flowName: 'a/b' }), '#/explore?flow=a%2Fb')
  assert.equal(stepHref('done'), null)
})

test('onStepPage ignores query strings and trailing slashes', () => {
  assert.ok(onStepPage('#/flows/new', '#/flows/new'))
  assert.ok(onStepPage('#/explore?flow=x', '#/explore'))
  assert.ok(onStepPage('#/', '#/'))
  assert.ok(onStepPage('', '#/'))
  assert.ok(onStepPage('#/anything', null))
  assert.ok(!onStepPage('#/flows', '#/flows/new'))
})

test('docs links point into the repository docs folder', () => {
  assert.equal(docsUrl('observability.md'), 'https://github.com/wcrum/gofi/blob/main/docs/observability.md')
  assert.equal(docsUrl('observability.md', 'prometheus'), 'https://github.com/wcrum/gofi/blob/main/docs/observability.md#prometheus')
})

// --- environment-specific commands ---

test('driver kinds map to an ingress kind and a default host', () => {
  assert.equal(ingressKind('kubernetes'), 'kubernetes')
  assert.equal(ingressKind('docker'), 'docker')
  assert.equal(ingressKind('inprocess'), 'host')
  assert.equal(ingressKind(undefined), 'host')
  assert.equal(defaultHost('inprocess'), '127.0.0.1')
  assert.equal(defaultHost('docker'), '127.0.0.1')
  assert.equal(defaultHost('kubernetes'), undefined)
})

const placeholders = { nodeAddress: '<node>', nodePort: '<node-port>', loadBalancerAddress: '<lb>', gatewayAddress: '<gateway>', clusterService: '<service>' }

test('binary and Docker flows are reached on the host at the listen port', () => {
  assert.deepEqual(resolveIngress({ driverKind: 'inprocess', listen: { network: 'tcp', port: 9000 } }, placeholders), { host: '127.0.0.1', port: 9000, via: 'host' })
  assert.deepEqual(resolveIngress({ driverKind: 'docker', listen: { network: 'udp', port: 514 }, host: 'docker.lan' }, placeholders), { host: 'docker.lan', port: 514, via: 'docker' })
})

test('Kubernetes exposure decides the address and port', () => {
  const tcp = { network: 'tcp', port: 9000 }
  assert.deepEqual(resolveIngress({ driverKind: 'kubernetes', listen: { ...tcp, service: { type: 'NodePort', nodePort: 30101 } } }, placeholders), { host: '<node>', port: 30101, via: 'node-port', exposure: 'NodePort' })
  assert.deepEqual(resolveIngress({ driverKind: 'kubernetes', listen: { ...tcp, service: { type: 'NodePort' } }, assignedNodePort: 31555, host: '10.0.0.5' }, placeholders), { host: '10.0.0.5', port: 31555, via: 'node-port', exposure: 'NodePort' })
  assert.deepEqual(resolveIngress({ driverKind: 'kubernetes', listen: { ...tcp, service: { type: 'NodePort' } } }, placeholders), { host: '<node>', port: '<node-port>', via: 'node-port', exposure: 'NodePort' })
  assert.deepEqual(resolveIngress({ driverKind: 'kubernetes', listen: { ...tcp, service: { type: 'LoadBalancer', loadBalancer: { ip: '192.0.2.10' } } } }, placeholders), { host: '192.0.2.10', port: 9000, via: 'load-balancer', exposure: 'LoadBalancer' })
  assert.deepEqual(resolveIngress({ driverKind: 'kubernetes', listen: { ...tcp, service: { type: 'Gateway', gateway: { namespace: 'istio', name: 'tenant', port: 7000 } } } }, placeholders), { host: '<gateway>', port: 7000, via: 'gateway', exposure: 'Gateway' })
  assert.deepEqual(resolveIngress({ driverKind: 'kubernetes', listen: tcp }, placeholders), { host: '<service>', port: 9000, via: 'cluster-ip', exposure: 'ClusterIP' })
})

test('the test command sends one line with netcat; UDP adds -u; QUIC has none', () => {
  assert.equal(testCommand('tcp', '127.0.0.1', 9000), `printf '%s\\n' '${SAMPLE_FRAME}' | nc 127.0.0.1 9000`)
  assert.equal(testCommand('udp', '10.0.0.5', '<node-port>'), `printf '%s\\n' '${SAMPLE_FRAME}' | nc -u -w1 10.0.0.5 <node-port>`)
  assert.equal(testCommand('quic', '127.0.0.1', 9000), null)
  assert.equal(testCommand('tcp', 'h', 1, `it's`), `printf '%s\\n' 'it'\\''s' | nc h 1`)
  assert.equal(shellQuote('plain'), `'plain'`)
})

test('port-forward targets the flow Pod by its listener label', () => {
  assert.equal(portForwardCommand('tutorial-events', 9000), `kubectl port-forward -l gofi.io/listener='tutorial-events' 9000:9000`)
  assert.equal(portForwardCommand('x', 7000, 'gofi'), `kubectl -n 'gofi' port-forward -l gofi.io/listener='x' 7000:7000`)
})

// --- storage ---

function memoryStorage() {
  const map = new Map()
  return { getItem: key => map.has(key) ? map.get(key) : null, setItem: (key, value) => map.set(key, String(value)), removeItem: key => map.delete(key), map }
}

test('first visit: no state, opens at step 1', () => {
  const storage = memoryStorage()
  assert.equal(readTutorialState(storage), null)
  assert.ok(shouldAutoOpen(null))
  assert.equal(resumeStep(null), 1)
})

test('state round-trips through storage under the versioned key', () => {
  const storage = memoryStorage()
  writeTutorialState({ status: 'active', step: 3 }, storage)
  assert.equal(storage.map.get(TUTORIAL_STORAGE_KEY), '{"status":"active","step":3}')
  assert.deepEqual(readTutorialState(storage), { status: 'active', step: 3 })
  clearTutorialState(storage)
  assert.equal(readTutorialState(storage), null)
})

test('skipped and finished tutorials stay closed; active ones reopen where they were', () => {
  assert.ok(shouldAutoOpen({ status: 'active', step: 4 }))
  assert.ok(!shouldAutoOpen({ status: 'dismissed', step: 4 }))
  assert.ok(!shouldAutoOpen({ status: 'done', step: 6 }))
  assert.equal(resumeStep({ status: 'active', step: 4 }), 4)
  assert.equal(resumeStep({ status: 'dismissed', step: 2 }), 2)
  assert.equal(resumeStep({ status: 'done', step: 6 }), 1)
})

test('garbage in storage reads as a first visit; a bad step becomes 1', () => {
  const storage = memoryStorage()
  storage.setItem(TUTORIAL_STORAGE_KEY, 'not json')
  assert.equal(readTutorialState(storage), null)
  storage.setItem(TUTORIAL_STORAGE_KEY, '{"status":"weird","step":2}')
  assert.equal(readTutorialState(storage), null)
  storage.setItem(TUTORIAL_STORAGE_KEY, '{"status":"dismissed","step":"x"}')
  assert.deepEqual(readTutorialState(storage), { status: 'dismissed', step: 1 })
})

test('storage that throws is survived', () => {
  const broken = { getItem() { throw new Error('blocked') }, setItem() { throw new Error('blocked') }, removeItem() { throw new Error('blocked') } }
  assert.equal(readTutorialState(broken), null)
  assert.doesNotThrow(() => writeTutorialState({ status: 'done', step: 6 }, broken))
  assert.doesNotThrow(() => clearTutorialState(broken))
  assert.equal(readTutorialState(null), null)
})

// --- example flow ---

test('the example is a TCP listen flow parsing JSON lines to stdout', () => {
  const flow = tutorialExampleFlow('inprocess')
  assert.equal(flow.name, TUTORIAL_FLOW_NAME)
  assert.deepEqual(flow.source, { listen: { network: 'tcp', port: TUTORIAL_FLOW_PORT } })
  assert.equal(flow.framing.mode, 'line')
  assert.equal(flow.parser.format, 'json')
  assert.deepEqual(flow.sinks, [{ type: 'stdout' }])
  assert.deepEqual(flow.stages, [])
  assert.deepEqual(tutorialExampleFlow('docker').source.listen.service, undefined)
  assert.deepEqual(tutorialExampleFlow('kubernetes').source.listen.service, { type: 'NodePort' })
  assert.deepEqual(tutorialExampleFlow('inprocess', [tutorialTestCase('Sample')]).testCases, [{ id: 'tutorial-sample', name: 'Sample', raw: SAMPLE_FRAME }])
  assert.doesNotThrow(() => JSON.parse(SAMPLE_FRAME))
})

test('?tutorial=1 is recognised before or inside the hash', async () => {
  const { tutorialRequested } = await import('./model.ts')
  assert.ok(tutorialRequested({ search: '?tutorial=1', hash: '#/flows' }))
  assert.ok(tutorialRequested({ search: '', hash: '#/flows?tutorial=1' }))
  assert.ok(tutorialRequested({ search: '', hash: '#/?flow=a&tutorial=1' }))
  assert.ok(!tutorialRequested({ search: '?tutorial=0', hash: '#/' }))
  assert.ok(!tutorialRequested({ search: '', hash: '' }))
})
