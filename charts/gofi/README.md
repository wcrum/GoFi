# GoFi Helm chart

Installs the GoFi portal/controller, namespaced permissions, and the `Flow`,
`Sandbox`, and `CelExpression` CRDs (`gofi.wcrum.dev/v1alpha1`). The default image is
`ghcr.io/wcrum/gofi:0.2.0`.

```sh
helm upgrade --install gofi ./charts/gofi --namespace gofi --create-namespace
kubectl -n gofi port-forward svc/gofi 9090:9090
```

Open `http://localhost:9090`. UDS deployments expose this Service through the
admin tenant gateway and configure portal SSO in the companion UDS package.
The standalone chart expects the operator to provide any required authentication.

## Declarative flows

Kubernetes mode persists portal edits to namespaced `Flow` resources and
reconciles externally applied changes every 30 seconds. Controller restarts
reload these resources. A resource's `metadata.name` identifies the flow; its
`spec` contains the pipeline definition without a redundant `name`. GoFi reads
only `kind: Flow`; see [docs/flows.md](../../docs/flows.md).

```yaml
apiVersion: gofi.wcrum.dev/v1alpha1
kind: Flow
metadata:
  name: telemetry
  namespace: gofi
spec:
  source:
    listen:
      port: 9000
      network: tcp
      service:
        type: NodePort
        nodePort: 31000
  framing:
    mode: line
  parser:
    format: json
  stages:
    - id: active
      kind: filter
      type: cel
      expression: fields.active == true
  sinks:
    - type: tcp
      url: tcp://receiver.apps.svc.cluster.local:9001
      delimiter: "\n"
```

The data path is `node:31000 → listener Service → listener Pod → receiver`.
Source Services can use `ClusterIP` or `NodePort`; omitting `source.service`
leaves ingress private to the listener Pod. Sink URLs address containers,
Services, or other explicit destinations. Kubernetes selectors stay internal
to GoFi. The portal discovers Services in the controller's own namespace,
including NodePort and LoadBalancer addresses; other namespaces can be
addressed explicitly by DNS if network policy permits.

For Helm-owned flows, supply the same `spec` plus `name` under the `flows`
values array. For independently managed flows, apply `Flow` YAML separately.
Helm release values and external GitOps should have a single declared owner for
each resource. Stage and sink objects preserve their fields in the CRD;
source, framing, and parser settings have structural schemas.

## Controller and exposure settings

- `replicaCount` defaults to `1`. More than one replica requires
  `leaderElection.enabled: true`: controllers coordinate through a
  `coordination.k8s.io` Lease (`leaderElection.leaseName`, default the release
  fullname; `leaseDuration`/`renewDeadline`/`retryPeriod`), only the holder
  reconciles, and every replica serves the portal and API. With leader election
  the Deployment rolls (`strategy.rollingUpdate`, surge 1 / unavailable 0);
  without it the strategy is `Recreate` so two controllers never overlap.
- `podDisruptionBudget` (minAvailable 1) renders only when `replicaCount > 1`;
  `podAntiAffinity: soft|hard` spreads replicas across nodes unless `affinity`
  is set; `topologySpreadConstraints` is passed through.
- `priorityClasses.create` installs the cluster-scoped `gofi-control-plane`
  and `gofi-dataplane` PriorityClasses (from exactly one release per cluster);
  the controller then uses the first and listener/worker Pods the second unless
  `priorityClassName` / `listenerPods.priorityClassName` / `workers.priorityClassName`
  say otherwise.
- `probes`: a `startupProbe` and `readinessProbe` on `/readyz` (ready once the
  informer caches have synced and the flow store has loaded) and a
  `livenessProbe` on `/healthz`. `goRuntime` sets `GOMEMLIMIT`
  (`goMemLimitRatio` of `resources.limits.memory`) and `GOMAXPROCS`
  (`limits.cpu`, rounded up) from the container limits.
- `image.repository`/`tag` select both controller and listener images;
  `image.digest` (and `sandboxImage.digest`) pin by digest for production.
  `image.pullPolicy` defaults to `IfNotPresent`; `values/dev-values.yaml`
  sets `Always` for dev clusters that rebuild one tag. `toolingImage` points the
  private format/validate worker at the `tooling` image so Sandbox workers can
  run the slim `worker` image (see `python/Dockerfile`).
- `service.type` supports `ClusterIP` (default), `NodePort`, and `LoadBalancer`.
  Set `service.nodePort` for a fixed public portal port; `service.annotations`,
  `service.loadBalancerIP`, `service.loadBalancerSourceRanges`, and
  `service.allocateLoadBalancerNodePorts` configure controller Service exposure.
  Data ingress is configured per flow (`source.listen`) instead.
- `exposure.loadBalancer.allocateNodePorts` (default `false`) and
  `exposure.loadBalancer.annotations` (e.g. `metallb.io/allow-shared-ip`) apply
  to every flow Service of type LoadBalancer. `gateway.maxListeners` (64) caps
  the listeners GoFi adds to one Gateway.
- `rbac.create` grants namespaced Pod lifecycle (including `patch`, which
  stamps the activation annotation), Service lifecycle/discovery, Flow,
  Sandbox, and CelExpression CRUD/watch access, TCPRoute/UDPRoute lifecycle
  and watch (the driver reads routes from an informer), Leases when leader
  election is on, Deployments when workers run with `replicas > 1` or
  autoscale, and the `gofi-flows` UDS Package when `uds.manageAllows` is on.
  `discovery.clusterWide` and `gateway.manageListeners` add the cluster roles
  documented above. Set `serviceAccount.create/name` and `rbac.create=false` to
  provide your own equivalent account and permissions.
- Controller Pods run as UID/GID `65532`, drop Linux capabilities, use a read-only
  root filesystem, and expose `/healthz` and `/readyz` probes. Flow runtime Pods use
  the same UID with no mounted Kubernetes API token.
- `profile` (`edge`, `default`, `large`; empty keeps the binary's default) is
  passed as `GOFI_PROFILE` to the controller and every listener Pod. It presets
  every `GOFI_*` tunable in [docs/configuration.md](../../docs/configuration.md):
  frame and decode budgets, connection caps, sink queue/batch/retry/breaker
  settings, spool size, and the portal's live-stream caps
  (`GOFI_WATCH_MAX_STREAMS`, `GOFI_WATCH_QUEUE`, `GOFI_HUB_IDLE_GRACE`, ...).
- `env` adds controller variables (e.g. `GOFI_WATCH_MAX_STREAMS_PER_CLIENT`,
  `GOFI_HUB_RESYNC`, or any tunable for in-process flows) on top of the
  profile; chart-owned runtime/image/namespace variables and `GOFI_PROFILE`
  are reserved. `resources`, `podAnnotations`, `nodeSelector`, `tolerations`,
  `affinity`, `topologySpreadConstraints`, `priorityClassName`, and
  `terminationGracePeriodSeconds` configure the controller Pod.

## Flow runtime and worker Pod defaults

`listenerPods` and `workers` set what every Pod the controller creates gets:
`resources` (requests/limits including `ephemeral-storage`; worker memory
comes from each Sandbox's `memoryMib`), `priorityClassName`, `nodeSelector`,
`tolerations`, `affinity`, `topologySpreadConstraints`,
`terminationGracePeriodSeconds`, and `probes` (startup/readiness/liveness
periods; listener probes hit `/healthz` on the `:9091` relay, worker probes
`/healthz` on `:8080`). The chart passes them to the controller as JSON in
`GOFI_LISTENER_RESOURCES`, `GOFI_LISTENER_SCHEDULING`, `GOFI_LISTENER_PROBES`,
`GOFI_WORKER_RESOURCES`, `GOFI_WORKER_SCHEDULING`, `GOFI_WORKER_PROBES`, and
`GOFI_WORKER_REPLICAS` (see [docs/deployment.md](../../docs/deployment.md)).

`listenerPods.extraEnv` and `workers.extraEnv` are lists of Kubernetes
`EnvVar` objects (`name` with `value` or `valueFrom`) appended to every listener
or worker container (passed as `GOFI_LISTENER_ENV` / `GOFI_WORKER_ENV`; the
chart prepends `GOFI_PROFILE` from `profile` to the listener list). Use them for
the data-plane tunables a flow runtime reads at start, such as
`GOFI_MAX_CONNECTIONS`, `GOFI_SINK_QUEUE_DEPTH`, `GOFI_SINK_SPOOL_DIR` and
`GOFI_SINK_SPOOL_MAX_BYTES`, `GOFI_MAX_DECODED_BYTES_PER_MESSAGE`, or
`GOFI_TAP_MAX_BYTES`. Names the controller sets itself (`FLOW_SPEC`,
`GOFI_K8S_NAMESPACE`, the worker's source and token, `GOMEMLIMIT`) keep their
values. `listenerPodTemplate.env` remains for sidecar-related variables.

```yaml
profile: edge
listenerPods:
  extraEnv:
    - name: GOFI_MAX_CONNECTIONS
      value: "256"
    - name: GOFI_SINK_SPOOL_DIR
      value: /var/lib/gofi/spool
```

`workers.replicas > 1` or `workers.autoscaling.enabled` makes the controller
run each library's workers as a Deployment behind its Service; the chart then
adds a PodDisruptionBudget (`workers.podDisruptionBudget`) and one
`HorizontalPodAutoscaler` per library in `workers.autoscaling.libraries`.

## Network policies

`networkPolicy.enabled` renders NetworkPolicies for an enforcing CNI:

- `blockMetadata`: controller and listener Pods may reach anywhere except the
  cloud metadata service (`169.254.169.254/32`, `fd00:ec2::254/128`). Python
  workers already carry a deny-all egress policy per library and are left out
  so the allow rule cannot widen them.
- `relay`: listener Pods accept flow data on every port but the `:9091` relay
  (taps, logs, metrics) only from the controller, the namespaces in
  `relay.scraperNamespaceSelectors` (default `monitoring`), and `relay.extraFrom`.

Keep it off on UDS Core: the Package already default-denies, and an
allow-all-except rule would widen it.

## Alerting

`alerting.enabled` installs a PrometheusRule. Besides the per-flow rules
(`docs/observability.md`), `alerting.controller` adds `GoFiControllerNoLeader`,
`GoFiControllerLeaderFlapping`, `GoFiControllerWatchErrors`,
`GoFiControllerReconcileStale`, `GoFiControllerAPIThrottled` (from the
standard `rest_client_rate_limiter_duration_seconds` the controller exports),
and `GoFiPodRestarts`; `alerting.streaming` adds `GoFiLiveViewersDropping`,
`GoFiStreamCapReached`, and `GoFiRelayReconnectStorm` for the portal's
live-stream hub. Every alert carries a `runbook_url`
(`alerting.runbookBaseUrl` + lower-cased alert name) pointing at the
[runbooks](../../docs/deployment.md#runbooks).

## Flow runtime sidecars and mounted tokens

`listenerPodTemplate` applies independently to every flow runtime Pod. Its fields
are `containers` (additional sidecars), `volumes`, `volumeMounts` (main GoFi
container mounts), and `env` (main GoFi container environment). `FLOW_SPEC`
is reserved. Provide every sidecar image explicitly and ensure it supports the
Pod's non-root security settings.

```yaml
listenerPodTemplate:
  containers: [] # Supply your provider's agent image and configuration here.
  volumes:
    - name: aster-token
      secret:
        secretName: gofi-aster-token
        defaultMode: 0440
  volumeMounts:
    - name: aster-token
      mountPath: /run/secrets/aster
      readOnly: true
  env:
    - name: GOFI_ASTER_TOKEN_FILE
      value: /run/secrets/aster/token
```

Mount the same Secret into a provider sidecar as required by that provider.
Stateful sidecars need independent state for each listener: use generic
ephemeral or CSI ephemeral volumes for per-Pod claims. A shared ReadWriteOnce
PVC is unsuitable for independent listener Pods. No provider image or storage
claim is installed by default. See the repository's Aster integration guide
for the loopback publish contract and agent setup.

## Validation and upgrades

```sh
helm lint charts/gofi --strict
helm template gofi charts/gofi --namespace gofi --include-crds
scripts/chart-test.sh   # lint, render every values overlay, helm-unittest suites
```

`charts/gofi/tests/*_test.yaml` are [helm-unittest](https://github.com/helm-unittest/helm-unittest)
suites (`helm unittest charts/gofi`, or the `helmunittest/helm-unittest`
image the script falls back to).

Helm installs CRDs on initial installation and retains them during uninstall;
it does not upgrade existing CRDs automatically. Apply the current CRD before
an upgrade that changes its schema:

```sh
kubectl apply --server-side -f charts/gofi/crds/
helm upgrade gofi ./charts/gofi --namespace gofi
```

Removing the Helm release retains the CRDs and their stored Flow resources.
