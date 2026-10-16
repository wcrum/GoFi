# Verification record

Updated 2026-10-16. This records executed checks separately from remaining
deployment gates. The working tree contains the combined changes for this
session; image refreshes and package rebuilds can lag the source.

## Source and protocol checks

- `GOWORK=off GOTOOLCHAIN=auto GOCACHE=/private/tmp/gofi-go-cache go test -race ./...`
  passed after the Python tooling RPCs and binding-reconciliation fix. The
  sandbox initially denied local socket binds; the successful run used approved
  execution outside that sandbox.
- Controller regression checks preserve freshly created Python listeners when
  worker discovery changes, retain them during worker startup, and replace only
  listeners with stale private bindings. Docker/Kubernetes environment checks
  distinguish referenced credential rotation from unrelated library additions.
- Generated Go Connect tests exercise listener CRUD, streaming and cancellation,
  simulation, discovery, real TCP/UDP replay bytes, Python function chaining,
  and sink error paths.
- Python worker tests pass for registration, invocation, authentication,
  deadlines, formatting, and static validation. Formatting/validation sentinel
  checks prove the submitted source is not executed by those actions.
- Real Docker worker tests pass using generated Go clients: custom parsing,
  Ruff formatting, undefined-name diagnostics, and runtime isolation checks.
- UI model/topology tests and the production build passed with actual Grafana
  components. Official icon assets and the Monaco worker are bundled locally.
- Protobuf generation and `buf build` pass. Existing naming-convention findings
  mean `buf lint` is not reported as passing.
- `actionlint` and `git diff --check` pass.

## Real application environment

The persistent Compose environment exposes the portal on `localhost:19090`, an
explicit-request sender on `19080`, a receiver on `19081`, and Prometheus on
`19094`. Its real TCP listener uses port `17000`. No background traffic generator
is required or started.

The initial end-to-end check persisted event
`88fe0325-81a5-4546-b265-ade172448f71` with the expected `observedBy: gofi`
transformation and both Prometheus scrape targets up. A full Sandbox control-plane
check created a temporary library and Docker listener, discovered registered
functions, and persisted event `35d4d14e-d03d-471a-8de7-83a39578ae67` with the
expected custom parser/transformer output. Temporary test resources were removed.
The latter run preceded the binding-race fix. After the final image refresh,
baseline event `af5abe83-f7e3-4f8a-8da4-8f7e3b66e9b3` persisted with the expected
transformation and both Prometheus targets up. Python event
`d299dab8-cdc2-4e8f-be5d-401db896e129` persisted through the custom parser and
transformer without the former startup settling delay. Receiver total reached
four records; temporary test flow/library resources were removed.

The final generated-client outer tooling check passed FormatSource,
ValidateSource, source nonexecution, private worker visibility, RunFunction,
explicit rejection, kind mismatch, and cleanup. Current local arm64 image
indexes are main
`sha256:b17e3c1873b5132e00bbb71afe4b8c9e549e005876abec59076a8e7f3453e924`
and Python worker
`sha256:afe55d6ad831935ac99e0a0448388e3b6bbf02e8aa614c4459c617ffd91ee838`.

See [the test environment instructions](test-environment.md) for commands that
send explicit, uniquely identified events and inspect receiver-side evidence.

## Browser evidence

The Grafana component migration was checked in a browser for official icon
loading, full-width cards, 4px rectangular geometry, stable flow layout,
Edit/Save/Cancel, and retaining unsaved edits across inventory polling. The
dashboard/live-flow theme consolidation and new Python editor actions require
their final checks against the refreshed image.

## Package and deployment gates

Both Helm charts pass strict lint. Rendered Listener/Sandbox schemas and UDS
Package configuration were checked against the repository-local schemas; Zarf
lint passes with image-pinning warnings. An initial arm64 Zarf package and test
bundle were created successfully. Final source/worker image changes require a
new package and bundle build.

Mandatory UDS MCP `validate-package` remains blocked by the server's skill
integrity guard, which requests `uds-mcp update` followed by reconnecting the MCP
server. Helm/schema/Zarf checks do not replace that gate. No authenticated UDS
deployment at `gofi.admin.uds.dev`, Kubernetes worker isolation, or live
Kubernetes networking qualification is claimed. The available local Kubernetes
context was unreachable during this session.

Security scans are not all green. The dependency findings, affected scope, and
remaining work are recorded in [the security scan assessment](security-scan.md).
