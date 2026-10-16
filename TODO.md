# GoFi implementation checklist

This is the running checklist for the requests and corrections in this session.
An item is checked only after its implementation and relevant verification are
complete. The local demo can lag source changes until the next successful build.

## Current priorities

- [ ] **UI-13 — One dashboard and live-flow theme.** Review `../cnak` read-only for shared shell/token patterns. Use the same GoFi navy/teal surfaces, typography, borders, spacing, controls, and radius across the overview, listener flow, editor, and inspector. Verify both views in the browser.
- [ ] **PY-07 — Python formatting, validation, and Run.** Add editor actions backed by typed ConnectRPCs and isolated workers, with source diagnostics and invocation results. Formatting/validation must work before a library has been saved.
- [x] **UI-01 — Use a real component library.** Adopted actual `@grafana/ui` components and its theme system. Official icon assets are bundled locally. Browser checks passed; navy/teal styling and the left sidebar are retained.
- [x] **UI-02 — Edit existing flows.** Edit, Save, and Cancel retain the listener name and persist to its original resource. Browser checks verified unchanged save, cancel, and retaining unsaved edits through polling.
- [ ] **PY-01 — Complete Python library integration.** Generated-RPC tests pass for a complete custom listener/parser/transformer/sink chain. Verify the saved-library portal journey against an actually launched worker.
- [ ] **OBS-01 — Structured live traffic.** Use Grafana Explore/Loki-style log rows, predictable columns, expandable fields and metadata, and useful filtering.
- [x] **DEMO-01 — Replace the seeded demo with a real integration environment.** Real sender application → GoFi → persistent receiver and Prometheus run on ports 19080/19090/19081/19094. Explicit baseline and Python pipeline events passed receiver-side verification after the final image refresh. No continuous fake-data generator is running. Reproducible lifecycle instructions are documented.

## Product and deployment

- [x] **DEP-01 — Preserve binary, Docker, and Kubernetes drivers.** Existing runtime paths retained; Docker socket use is explicitly allowed for Compose.
- [x] **DEP-02 — Portable container build.** Remove the external sibling design-system build context; build from this repository. Rootless application image; Compose controller may run as root for its Docker socket. Image build and container smoke test passed.
- [ ] **DEP-03 — Durable objects per deployment.** Binary defaults to a local resource file; Docker uses a mounted resource directory; Kubernetes uses namespace-scoped Listener and Sandbox CRDs. Verify defaults, restart recovery, and declarative reconciliation in each supported runtime.
- [x] **DEP-04 — Reject Kubernetes exposure in binary/Docker mode.** Do not silently accept ClusterIP/NodePort. Backend validation and binary-mode editor controls were verified.
- [ ] **DEP-05 — UDS admin portal.** Package Helm/Zarf/bundle sources with Ambient networking and Authservice; final in-cluster URL is `https://gofi.admin.uds.dev`. Complete package build, mandatory validation, and a real authenticated deployment test.
- [ ] **DEP-06 — Repository refresh.** Update dependencies/toolchains, add reproducible generation and CI, run the final combined checks. Keep Kubernetes transitive modules compatible rather than blindly selecting mismatched latest versions.

## Traffic and declarative resources

- [x] **NET-01 — NodePort ingress and explicit destinations.** Implement per-listener Kubernetes Services and direct HTTP/TCP/UDP destinations. Driver/socket tests passed.
- [x] **NET-02 — Endpoint inventory.** Discover available Services, LoadBalancer addresses, NodePorts, and container endpoints. Namespace-scoped Kubernetes discovery is implemented.
- [x] **NET-03 — Kubernetes selectors and replay.** Persist pod label selectors and destination ports, preview Ready pods, and replay bounded raw payloads to selected TCP/UDP targets. Backend resolution/replay tests passed; confirm final portal integration.
- [x] **NET-04 — Zenoh sink.** Official REST-router publishing, explicit picker labeling, and protocol/auth/error tests pass. Native Zenoh wire transport is not implemented.
- [x] **DATA-01 — Configurable decoding.** Ordered base64/base64url/hex/gzip/zlib/URL decoding before JSON/XML/text parsing, bounded output, and configurable parse-error handling. Tests passed.
- [x] **DATA-02 — Expression filters.** CEL and RE2-compatible regex, transforms, rate limits, deduplication, required fields, and per-sink predicates. Tests passed; retain final regression coverage.
- [ ] **DATA-03 — Declarative drift prevention.** `gofi.wcrum.dev/v1alpha1` Listener resources/file manifests remain authoritative, survive restarts, and reconcile changed runtime resources. Finish combined tests and deployed CRD verification.
- [x] **ASTER-01 — Aster integration.** Publish through authenticated loopback-only Aster ConnectRPC agent, verify receipts, preserve stable per-ingest operation keys, and document dedicated namespace/provider boundaries. HTTP protocol tests passed; live protected-provider deployment remains external/unverified.

## RPC and Python developer mode

- [x] **RPC-01 — Application backend uses ConnectRPC.** Typed Protobuf management, discovery, simulation, status, replay, and server-streaming traffic APIs; generated browser client. Legacy REST/SSE application routes retired. Health checks, static assets, and Prometheus scraping remain HTTP infrastructure endpoints.
- [x] **RPC-02 — Internal traffic relay uses ConnectRPC.** Local/remote stream fan-in and cancellation were tested, including more than six listeners.
- [x] **PY-02 — GoFi launches isolated workers.** Docker/Kubernetes worker backends with CPU/memory/deadline/output limits and restricted networking. Real Docker-to-generated-Connect parser integration passed. Kubernetes isolation has not been deployed/tested yet.
- [x] **PY-08 — Preserve fresh flows during worker discovery.** Compare each remote listener's actual private bindings with the referenced ready workers. Library additions no longer restart every Python listener. Controller and Docker/Kubernetes environment-comparison race tests passed; final real-environment regression remains part of QA.
- [x] **PY-03 — Decorator SDK.** `@gofi.port`, `@gofi.listener`, `@gofi.parser`, `@gofi.transformer`, and `@gofi.sink` register reusable functions. Python tests passed.
- [ ] **PY-04 — Select library functions in the builder.** Custom listener hooks, parsers, transformer stages, and sinks must appear beside built-in choices and round-trip through saved Protobuf/resources. UI registry selectors are implemented; verify the complete launched-worker workflow.
- [ ] **PY-05 — Library authoring.** Portal source editor, resource settings, save/launch, status, and delete; persist code in a local file or Sandbox CRD. Component and control RPCs are written; combined runtime verification pending.
- [ ] **PY-06 — Streamer/fetcher extension points.** Define and implement the additional requested developer actions with explicit execution/network semantics. Do not present these as implemented yet.

## Interface feedback

- [x] **UI-03 — Main overview includes all listeners.** Show listener ingress, processing, destinations, and dead letters; drill into one listener without losing the overview. Browser verification passed.
- [x] **UI-04 — Keep tools on the left and flow central.** Current sidebar layout is the accepted baseline.
- [x] **UI-05 — Stop jumpy connections.** Deterministic layout, fixed node dimensions, stable counters, no traffic-triggered refit. Browser geometry stayed unchanged while live counts advanced.
- [x] **UI-06 — Stable hover cursors.** Consistent edge/hit-area/pane cursors; use drag cursors only during gestures. Browser computed-style checks passed.
- [x] **UI-07 — Standardize roundness.** One 4px token for rectangular controls, panels, cards, nodes, and menus; circular status dots/handles retained. Browser checks passed; preserve through component-library migration.
- [x] **UI-08 — Fill the containing div.** Remove arbitrary maximum-width caps from listener, endpoint, and configuration panels. Browser card/content width equality passed.
- [ ] **UI-09 — Visible connection handles.** Keep handles outside card borders, above edges, and clear of connection labels. Final screenshot verification pending.
- [x] **UI-10 — Searchable grouped stage picker.** Group filters/validation, transforms/custom functions, traffic controls, and destinations; search names/descriptions/libraries. UI build/tests passed.
- [ ] **UI-11 — Grafana-style structured traffic inspector.** Preserve useful Pause/Clear/Test actions and make message structure easy to inspect.
- [ ] **UI-12 — Real traffic totals.** Label connection/session counts honestly; use Prometheus for process/runtime totals and history. A browser reconnect must not be presented as resetting the actual processing history.

## Observability and final evidence

- [x] **OBS-02 — Single-line structured operational logs.** JSON slog and HTTP request logs, structured stdout sink, and JSON Python runtime logs implemented and tested. Arbitrary user-authored Python print output remains user controlled.
- [x] **OBS-03 — Native Loki sink.** Native push payload, labels/timestamps, bounded requests, authentication, and error handling pass protocol tests. A production Loki deployment is not claimed.
- [x] **OBS-04 — Prometheus integration.** Real controller/listener scrape targets and actual processing counters verified in Compose. UDS monitor resources render and validate locally; in-cluster deployment remains DEP-05.
- [ ] **QA-01 — Final combined checks.** Go/race tests, Python tests, UI tests/build/audit, chart/CRD schemas, Zarf lint, rebuilt images, live demo, and screenshots.
- [x] **TRACK-01 — Maintain this checklist.** Requests and corrections are recorded with evidence and remaining gates; keep updating as work proceeds.

## UDS skill gates

The UDS MCP currently rejects requests because its installed skill integrity
check requires an update/reconnect. Helm/Zarf lint do not replace that gate.

- [x] ChartUrl — repository-owned chart identified.
- [x] ValidateChart — chart metadata inspected and Helm rendering/lint passed.
- [x] ScaffoldZarf — MCP unavailable; repository-owned charts/reference layout used.
- [x] ReviewFiles — root/common manifests and chart order reviewed.
- [x] LoadBuildReferences — requirements, schema, reference package, and background read.
- [x] AnalyzeValues — image paths and feature toggles reviewed.
- [x] DetermineDependencies — desired state uses Kubernetes/file storage; UDS base and identity layers required.
- [x] PinImages — application/worker semver references declared; local images built.
- [ ] VerifyHelmChart — re-render final charts after observability/Sandbox changes.
- [x] CreateZarfPackage — initial arm64 build passed; rebuild final source/images before deployment.
- [x] CreateUDSBundleConfig — flavor-neutral local bundle written.
- [ ] ConfigureTaskRunner — finish reproducible commands and check runner.
- [x] CreateUDSBundle — initial arm64 bundle passed; final artifact rebuild remains required.
- [ ] VerifyInputs
- [x] LoadIntegrationReferences
- [x] DetermineCoreLayers
- [ ] ImplementConfigChart
- [ ] ConfigureNetworkPolicies
- [ ] ConfigureIstio
- [ ] ConfigureSSO
- [ ] ConfigureMonitoring
- [ ] DocumentConfiguration
- [x] UpdateReleaserVersion — `0.2.0-uds.0` initialized.
- [ ] VerifyPackageRequirements
- [ ] RunInitialPackageValidation — **blocked: MCP skill integrity guard**.
- [ ] ResolvePackageValidationFindings
- [ ] ConfirmPackageValidationResolved
- [ ] DeployTestEnvironment — must follow successful mandatory validation.
- [ ] FinalPhaseAudit
- [ ] ReportSessionGaps
