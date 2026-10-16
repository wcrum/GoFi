# UDS package configuration

The application chart is `charts/gofi`. The separate `chart` directory contains
the UDS `Package` and scoped NodePort `Exemption`. `common/zarf.yaml` composes
these charts; `zarf.yaml` selects the upstream flavor and pins the GoFi and
Python worker image versions. `bundle/uds-bundle.yaml` packages GoFi for an
existing UDS Core installation with identity, authorization, and monitoring
layers enabled.

## Portal and SSO

Default UDS configuration places the controller behind the admin tenant gateway
at `https://gofi.admin.uds.dev`. The Package's SSO client is **GoFi Login**, with
Authservice enabled on the controller selector and a `/login` return route. The
portal's SPA serves that route after authentication. All GoFi and Sandbox RPCs
and live streams use the same protected origin.

These values configure the UDS chart:

```yaml
domain: uds.dev
gateway: admin
sso:
  enabled: true
  secretConfig:
    name: gofi-sso
```

`gateway: tenant` uses `https://gofi.uds.dev`. Change the domain and gateway
together with the platform's DNS, certificates, and gateway routing. Keep the
Authservice selector aligned with the actual controller labels. Disabling SSO
removes this package's authentication declaration; it does not create an
alternative application login service.

The controller Service remains ClusterIP. Listener data sockets are separate
application endpoints and do not inherit browser login semantics.

## Durable resources

`Listener` and `Sandbox` are namespaced resources in
`gofi.wcrum.dev/v1alpha1`. Portal changes persist through the Kubernetes API,
and declarative changes reload during reconciliation. No application Postgres,
Valkey, or object-store dependency is required: Kubernetes stores desired
configuration, and live traffic state remains in process. This is not a durable
message queue or historical traffic database.

Run one controller. The application chart rejects a different replica count
until controller leader election exists. Listener Pods have no service-account
token; their Kubernetes selector targets are represented by controller-managed
Services, so the data path does not require Kubernetes API credentials.

## NodePort and data networking

The UDS config chart can enable the `DisallowNodePortServices` exemption only for
Services named `gofi-listener-*` in the GoFi namespace. It covers explicitly
configured Listener data ingress. It does not exempt the controller Service or
Services in other namespaces. Set `nodePort.enabled: false` when NodePort is not
needed.

An exemption permits Service creation; it does not open a data path. Declare
site-specific ingress from approved source CIDRs and egress to actual receiver
ports through `additionalNetworkAllow`. For example, replace this documentation
CIDR with your approved sender range:

```yaml
additionalNetworkAllow:
  - description: Approved telemetry senders
    direction: Ingress
    selector:
      gofi.io/listener: telemetry
    remoteCidr: 192.0.2.0/24
    remoteProtocol: TCP
    port: 7000
```

Use the backend Pod listener port (`7000` here), not the allocated NodePort
(`31080`). For UDP use a separate entry with `remoteProtocol: UDP`. Cross-namespace
or external destinations also need explicit rules; selector destinations
currently remain in GoFi's namespace. For an external HTTP receiver, declare its
actual host and application port according to the installed UDS Core schema.
The [UDS network access guide](https://docs.defenseunicorns.com/core/v1-14/how-to-guides/networking/define-network-access/)
and [Package CR reference](https://docs.defenseunicorns.com/core/v1-14/reference/operator--crds/packages-v1alpha1-cr/)
describe these fields.

The package declares controller access to the Kubernetes API and intra-namespace
runtime communication. Python worker isolation also depends on avoiding broad
allow policies that select those workers: Kubernetes NetworkPolicy permissions
are additive. Review generated UDS policies alongside worker policies. Test
NodePort traffic against your installed mesh and admission configuration before
treating a rendered manifest as operational evidence.

Aster application traffic stays inside the listener's dedicated loopback
namespace; do not expose the agent with a Service or NodePort. Agent mesh
connectivity and provisioning remain deployment-owner responsibilities.

## Build and integration gates

Review the pinned images and make them available before creating air-gap
artifacts. From the repository root:

```sh
helm lint charts/gofi chart
helm template gofi charts/gofi --namespace gofi
helm template uds-gofi-config chart --namespace gofi
# After validate-package succeeds, build final artifacts:
uds zarf package create --flavor upstream --skip-sbom --confirm
uds run create:test-bundle
```

The test bundle expects an existing compatible UDS Core cluster with identity,
authorization, and monitoring layers. Run the required `validate-package` gate
before the final build and deployment; resolve its findings in source, then
rebuild the package and bundle. Only after that validation gate is clear should
the workflow reuse a healthy cluster and deploy the rebuilt artifacts, check Pod
readiness and restarts, and exercise SSO and application traffic. Configuration
fixes belong in source and rebuilt artifacts rather than live-cluster patches.
The blocked MCP validation gate leaves this workflow blocked before live UDS
deployment.

UDS MCP operations in this session were blocked by the server's skill-integrity
guard. Package source, schema rendering, and application tests do not satisfy
those integration or validation gates. No successful UDS deployment, protected
login, or zero-finding package validation is claimed. The final measured checks
and remaining gates are recorded in [verification evidence](verification.md).
