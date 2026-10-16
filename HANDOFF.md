# GoFi agent handoff

Checkpoint requested by the user on 2026-10-16. Stop ongoing feature work at this
point and use this document plus [TODO.md](TODO.md) to resume. Preserve the
user's existing changes and commits. The broad implementation is in commit
`2f698b4` (`feat: uds package, connectrpc, other things`); `2b0f711`
(`feat: continued work`) captures the additional theme refinements and tests.
The following checkpoint records final runtime evidence and handoff details.

## Run and inspect

The real persistent Compose test environment is running:

| Endpoint | Purpose |
| --- | --- |
| http://localhost:19090 | Current portal and application ConnectRPCs |
| TCP localhost:17000 | `real-events` listener ingress |
| http://localhost:19080 | Sender application; sends only on explicit request |
| http://localhost:19081/events | Persisted receiver events |
| http://localhost:19094 | Prometheus |

The controller listens on 9090 inside its container, published as 19090. An
older binary demo may still occupy host 9090, with `telemetry-demo` TCP 10002.
Use 19090 for the current UI. Continuous synthetic traffic generators were
stopped; do not restart them as the acceptance environment.

```sh
docker compose -f compose.test.yaml ps
python3 testenv/manage.py verify
python3 testenv/manage.py verify-python
PYTHONPATH=python /private/tmp/gofi-python-venv/bin/python scripts/verify_python_tools.py --api http://127.0.0.1:19090
```

The temporary virtualenv is local convenience, not a repository dependency.
Install `python/requirements.txt` in another virtualenv when needed. See
[docs/test-environment.md](docs/test-environment.md) for lifecycle commands.
Do not delete named volumes; they hold desired resources, receiver records,
and Prometheus history. GoFi's listener child containers are outside Compose;
use the documented stop helper rather than only `docker compose down`.

## Latest product direction

- Keep the current left sidebar, central flow, and original GoFi navy/teal
  identity. Use real Grafana components, consistent 4px rectangular geometry,
  and full-width content. Avoid another broad redesign.
- The user's latest correction was that the main dashboard and live-flow views
  had different themes. Source now derives Tailwind, Grafana, and React Flow
  colors from the same CSS tokens in `ui/src/index.css`, and their cards share
  Grafana `PanelContainer`. Final cross-view browser qualification is pending.
- `../cnak` was reviewed read-only: its useful pattern is semantic theme tokens
  shared by the shell and live-flow nodes. No CNAK code or build dependency was
  imported; the GoFi build must remain self-contained.
- Python authoring now uses Grafana's Monaco CodeEditor with a locally bundled
  worker, Format/Validate/Run controls, diagnostics, and sample inputs/results.
  Run invokes saved registered functions and is disabled for unsaved source.
- All application/backend and private-worker APIs use generated ConnectRPC.
  Health, static assets, and Prometheus scraping remain HTTP infrastructure.
- Deployments are binary, Docker, and Kubernetes. ClusterIP/NodePort settings
  are rejected in binary/Docker modes. Kubernetes destinations support scoped
  pod selectors and real TCP/UDP replay.
- UDS target remains `https://gofi.admin.uds.dev` behind the admin gateway and
  Authservice. No authenticated cluster deployment has been completed.

## Implementation landmarks

| Area | Location |
| --- | --- |
| Typed API contracts and generated code | `proto/`, `gen/`, `ui/src/gen/`, `python/gofi/v1/` |
| Python formatting/validation/Run RPCs | `api/source.go`, `api/source_test.go` |
| Private tooling-worker lifecycle | `sandbox/tooling.go` |
| Isolated Python SDK/runtime/static Ruff tooling | `python/gofi/` |
| Worker invocation and private bindings | `functionrpc/`, `driver/python.go`, `controller/python.go`, `sandbox_runtime.go` |
| Declarative desired state and drivers | `desired/`, `controller/`, `driver/` |
| UI theme and shared controls | `ui/src/index.css`, `ui/src/grafana-theme.ts`, `ui/src/components/primitives.tsx` |
| Flow overview/editor and Python authoring | `ui/src/components/canvas/`, `SandboxManager.tsx`, `PythonSourceEditor.tsx` |
| Real application environment | `compose.test.yaml`, `testenv/` |
| UDS packaging | `zarf.yaml`, `common/`, `chart/`, `charts/gofi/`, `bundle/` |

The binding-refresh race is fixed: discovering a new library used to delete
every remote Python listener, including one just created. Reconciliation now
compares each runtime's actual private environment with only its referenced
ready libraries and restarts stale listeners. Unavailable workers do not cause
blanket deletion. The old six-second settling workaround was removed from the
Python integration verifier.

Formatting and validation run statically in a private fixed-source tooling
worker, including before any user library exists. They must never import or
execute the submitted source. The latest Python change allows generic valid
Python source in validation; actual library launch still requires registered
GoFi functions. Check the image status below before interpreting smoke results.

## Verification and remaining gates

See [docs/verification.md](docs/verification.md) for measured evidence and
[docs/security-scan.md](docs/security-scan.md) for the non-green dependency scans.

Current local arm64 image indexes:

- `ghcr.io/wcrum/gofi:0.2.0`:
  `sha256:b17e3c1873b5132e00bbb71afe4b8c9e549e005876abec59076a8e7f3453e924`
- `ghcr.io/wcrum/gofi-python:0.2.0`:
  `sha256:afe55d6ad831935ac99e0a0448388e3b6bbf02e8aa614c4459c617ffd91ee838`

The controller on 19090 was refreshed with these images. The canonical
generated-client outer tooling verifier passed formatting, validation,
nonexecution, private-worker visibility, Run, explicit rejection, kind-error,
and cleanup checks against this runtime. A private controller-owned tooling
worker remains running normally; it is not a saved user library.

Final baseline event `af5abe83-f7e3-4f8a-8da4-8f7e3b66e9b3` persisted with the
expected GoFi transformation and both Prometheus targets up. Immediate Python
pipeline event `d299dab8-cdc2-4e8f-be5d-401db896e129` persisted with custom
parser/transformer output, without a settling delay. Receiver total reached
four records. Temporary flow/library resources were removed.

- Full Go race suite passed with local network access; focused latest API and
  supervisor tooling race tests also passed.
- UI 16 model/topology tests and production build passed after the shared-theme
  and Python editor changes. Final browser verification against the refreshed
  image is incomplete.
- Python eight tests and generated-Go-to-real-Docker worker checks passed for
  parser invocation, formatting, diagnostics, auth, deadline, and isolation.
- Real sender/receiver, outer tooling, and Python library pipeline verifiers
  passed after the final worker refresh, including without the settling delay.
- Strict Helm lint and `uds zarf dev lint --flavor upstream` pass; the latter
  warns that local application images use semver tags without digests.
- `buf build`, actionlint, and whitespace checks pass. `buf lint` has existing
  naming findings; do not claim it passes.
- Initial arm64 Zarf package and UDS test bundle exist, but predate the final
  images and must be rebuilt before deployment.
- Mandatory UDS MCP validation is blocked by its skill-integrity guard. It
  requests `uds-mcp update` and server reconnection; Helm/Zarf lint is not a
  substitute. The local Kubernetes context was unreachable. No live UDS or
  Kubernetes worker/network qualification is claimed.
- Streamer/fetcher Python extension points remain unimplemented and tracked.

UDS MCP runtime lives at
`../uds-packages/uds-mcp-runtime/bin/uds-mcp`; the configured server's workspace
root is `../uds-packages`, which may also need attention for this repository.
Updating global skill/server configuration requires the appropriate filesystem
approval. Read the installed `uds-context`/`uds-package` skills before continuing
that workflow, and preserve all mandatory validation gates.

## Immediate next steps

1. Confirm current image/worker status and run the explicit real verifiers above.
2. Browser-check dashboard → listener detail → editor → live inspector for
   matching surfaces, borders, fonts, radius, controls, visible handles, and
   stable connections. Test the new Python editor actions through the portal.
3. Update evidence and TODO checkboxes only after their relevant checks pass.
4. Rebuild final Zarf/bundle artifacts, resolve the UDS MCP validation blocker,
   and then qualify the authenticated admin-gateway deployment.

Do not push or publish a release solely because this checkpoint exists.

## Local browser tools

Vite 5173 proxies the old binary on 9090; Vite 5174 proxies the actual test portal
on 19090 (`GOFI_API_PROXY` overrides the target). A headless Chrome debugging
endpoint may be available at 9224. The CUA tool failed because enabled surfaces
were not configured.

Existing scripts `/private/tmp/gofi-grafana-edit-qa.mjs` and
`/private/tmp/gofi-topology-qa.mjs` assume the old telemetry demo. The former
saves an unchanged existing listener. Do not run it blindly against `real-events`.
Use a temporary flow/library for mutating browser checks and clean up only those
resources. Prior screenshots are `/private/tmp/gofi-grafana-overview.png`,
`/private/tmp/gofi-grafana-edit.png`, and `/private/tmp/gofi-grafana-logs.png`.
