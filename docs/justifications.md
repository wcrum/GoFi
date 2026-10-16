# Design choices and user journeys

| Choice | Reason and limit |
| --- | --- |
| Preserve the sidebar and GoFi flow language | Tools stay on the left and traffic stays central. Grafana-inspired navigation and dark surfaces refine the theme without replacing the product's existing flow identity. |
| Share one theme across dashboard and flow | The adjacent CNAK frontend uses semantic theme variables for the application shell, panels, and live-flow nodes. GoFi adopts that shared-token pattern while keeping its own navy/teal colors and actual Grafana components. Its build stays self-contained. |
| All-listeners overview | Administrators can see ingress listeners and routes together, then focus on one pipeline. A single server stream avoids per-listener browser connection limits. |
| ConnectRPC application boundary | One typed protobuf contract serves Go, browser, and Python clients, including server-streamed traffic. Static assets, health probes, and metrics retain ordinary HTTP semantics; external sink protocols retain their own APIs. |
| Durable resources per deployment | Kubernetes uses namespaced Listener/Sandbox CRDs. Binary and Docker use atomic YAML files by default. The same declared application contract can move between deployments without requiring Kubernetes networking in a local binary. |
| Validate a complete Listener snapshot | Invalid syntax, stages, parser/framing settings, destination configuration, or conditions cannot partially replace the last good desired state. Storage failures permit safe recovery while postponing destructive changes. |
| No application database dependency | Kubernetes already stores cluster desired state; files cover local deployments. This removes unnecessary Postgres/Valkey/Minio dependencies while explicitly excluding durable traffic history and outbox guarantees. |
| Driver-specific networking | Binary and Docker bind host TCP/UDP ports. Kubernetes supports ClusterIP/NodePort ingress, Service/LB discovery, and scoped selector destinations. Unsupported settings fail instead of being silently ignored. |
| Selector destinations use Services | Kubernetes maintains ready Pod membership. Stable per-listener Service names preserve the declared selector without embedding transient Pod IPs in desired state or granting listener Pods Kubernetes credentials. |
| Replay differs from simulation | Simulation evaluates a draft without sink delivery. Replay is an explicit real send of exact bytes, with bounded payload/time/fan-out and per-destination results. Neither implies downstream durable processing. |
| Isolated Python libraries | GoFi supervises resource-bounded worker containers/Pods and invokes named functions over authenticated ConnectRPC. The controller never evaluates submitted Python source. Listener hooks remain attached to real GoFi sockets. |
| CEL and RE2 as built-ins | Familiar expression tools cover common filtering and shaping without a worker. Static non-boolean predicates are rejected; dynamic values are checked when messages arrive. |
| Aster remains loopback-only | Aster's application agent owns provisioning, cryptographic authority, and local durable acceptance. GoFi supplies selected application payloads inside the agent's supported network namespace boundary. |
| Zenoh uses its REST router | A fixed PUT to a concrete key follows the documented router plugin contract. The adapter does not claim native Zenoh transport support. |
| Structured logs and bounded labels | Operational diagnostics stay separate from stdout traffic records. Prometheus/Loki integration exposes useful signals without automatically indexing arbitrary message fields as labels. |
| Retain the current Docker SDK API line | The source scan reports broad legacy-module daemon advisories. GoFi uses client/API packages, while the independently running Engine must receive daemon fixes. The exact non-green result and scope assessment are recorded in [the security scan](security-scan.md); a client module migration remains a separate API change. |
| One active controller | Durable configuration alone does not provide leader election. The chart enforces one replica until ownership coordination is implemented. |

## Journey checks

These scenarios map the requested stories to repeatable acceptance checks. They
are a checklist for qualification, not a claim that each browser or cluster
scenario has already run. Actual evidence belongs in [verification](verification.md).

| User journey | Acceptance check |
| --- | --- |
| Log in behind the UDS admin gateway | Visit `gofi.admin.uds.dev`, complete GoFi Login, return to the portal, and verify management and WatchTraffic share the protected origin. |
| Keep tools left and flow central | Open the all-listeners view, verify sources/stages/destinations and original GoFi flow styling, then select a listener and return. |
| Avoid declarative drift | Create a Listener via Connect, edit its file/CRD externally, observe reconciliation, remove a runtime, and verify recovery. Submit an invalid multi-resource snapshot and verify no partial replacement. |
| Receive encoded application traffic | Send valid and malformed encoded frames; verify decoded fields, preserved built-in decoder wire bytes, parser error policy, and dead-letter attribution. |
| Filter using common expressions | Exercise CEL comparisons/presence checks and RE2 field/raw matches, including rejection and runtime error behavior. |
| Deploy binary, Docker, or Kubernetes | Verify durable configuration, capability-specific errors, listener connectivity, and runtime status in each supported driver. |
| Select Kubernetes Pods by labels | Preview ready matches, save a selector sink, change matching Pods, verify Service membership/drift repair, and reject another namespace. |
| Replay selected traffic | Send exact TCP/UDP bytes to a direct endpoint and to multiple ready matches; verify framing, per-endpoint outcomes, size/time limits, and zero-match refusal. |
| Extend arbitrary application handling | Create a Python library, inspect worker status, choose registered source/parser/transformer/sink functions, exercise deadline/auth failures, and verify worker isolation. |
| Publish through Aster or Zenoh | Verify each documented protocol contract with its configured agent/router; distinguish local acceptance or successful writes from end-to-end application consumption. |

Go tests cover resource persistence, validation, reconciliation, Connect codecs
and streams, driver behavior, sink protocols, and replay. UI tests cover model
conversion and adapters; Python tests cover worker registration, authentication,
invocation, and deadline behavior. A real UDS login, mesh/network path, and final
package validation remain separate integration gates.
