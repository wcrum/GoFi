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

- `replicaCount` is fixed at `1`. Upgrade strategy is `Recreate`; durable desired
  state does not provide leader election between concurrent controllers.
- `image.repository` and `image.tag` select both controller and listener images.
- `service.type` supports `ClusterIP` (default), `NodePort`, and `LoadBalancer`.
  Set `service.nodePort` for a fixed public portal port; `service.annotations`,
  `service.loadBalancerIP`, and `service.loadBalancerSourceRanges` configure
  controller Service exposure. Data ingress is configured per flow (`source.listen`) instead.
- `rbac.create` grants namespaced Pod lifecycle, Service lifecycle/discovery, and
  Flow, Sandbox, and CelExpression CRUD/watch access. No cluster-wide discovery permission is
  installed. Set `serviceAccount.create/name` and `rbac.create=false` to provide
  your own equivalent account and permissions.
- Controller Pods run as UID/GID `65532`, drop Linux capabilities, use a read-only
  root filesystem, and expose `/healthz` and `/readyz` probes. Flow runtime Pods use
  the same UID with no mounted Kubernetes API token.
- `env` adds controller variables; chart-owned runtime/image/namespace variables
  are reserved. `resources`, `podAnnotations`, `nodeSelector`, `tolerations`, and
  `affinity` configure the controller Pod.

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
```

Helm installs CRDs on initial installation and retains them during uninstall;
it does not upgrade existing CRDs automatically. Apply the current CRD before
an upgrade that changes its schema:

```sh
kubectl apply --server-side -f charts/gofi/crds/
helm upgrade gofi ./charts/gofi --namespace gofi
```

Removing the Helm release retains the CRDs and their stored Flow resources.
