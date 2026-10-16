# Go dependency scan

## Result

A source scan on 2026-10-16 used Go 1.27.2 and `govulncheck` v1.9.0 against the
official `https://vuln.go.dev` database:

```sh
GOWORK=off GOTOOLCHAIN=auto govulncheck ./...
```

The command exited **3**, reporting two symbol-level findings from the retained
`github.com/docker/docker v28.5.2+incompatible` module. It also reported four
additional module-level findings that were not identified as called by GoFi.
This is not a clean vulnerability scan.

| Go advisory | Scanner finding | Upstream scope |
| --- | --- | --- |
| [GO-2026-4887](https://pkg.go.dev/vuln/GO-2026-4887), CVE-2026-34040 | Moby AuthZ plugin bypass with oversized request bodies; no fixed version recorded for the legacy module path | [Docker Engine authorization-plugin body forwarding](https://github.com/moby/moby/security/advisories/GHSA-x744-4wpc-v9h2), fixed in Engine 29.3.1 |
| [GO-2026-4883](https://pkg.go.dev/vuln/GO-2026-4883), CVE-2026-33997 | Moby off-by-one plugin privilege validation; no fixed version recorded for the legacy module path | [Docker Engine plugin-install privilege comparison](https://github.com/moby/moby/security/advisories/GHSA-pxq6-2prw-chj9), fixed in Engine 29.3.1 |

Both Go database entries are marked **unreviewed** and describe the entire
legacy `github.com/docker/docker` module as affected. Scanner traces include
client/API package initialization and ordinary client methods such as container
create/list/inspect/start/stop/remove and network operations.

## Applicability and retained dependency

The resolved application package graph was inspected with
`go list -deps . | rg '^github.com/docker/docker/'`. It contains only Docker
`client`, `api`, and `api/types/...` packages, with no Docker daemon, API server,
AuthZ, or plugin implementation packages. GoFi uses those client/API types to
talk to an independently running Docker-compatible daemon. It does not run Docker Engine, implement its
AuthZ-plugin request handling, or install plugins. The upstream advisories
identify those daemon-side mechanisms as the affected implementations. Our
assessment is that the broad legacy-module annotations cause the checker to
attribute these daemon advisories to GoFi's client calls; the reported traces do
not demonstrate that GoFi contains the affected daemon implementations.

The current Docker SDK module is therefore retained pending a deliberate API
migration. Replacing the Go client module would not patch the host Docker Engine.
Operators using the affected daemon features should run Engine 29.3.1 or later
with the applicable upstream fixes. This repository scan does not determine the
installed daemon version or validate its configuration; host runtime evidence is
separate from this source assessment.

The dependency refresh migrated CEL to its current module path,
`cel.dev/cel-go v0.32.0`, verified through `go list -m ...@latest`, and removed the
old `github.com/google/cel-go` module. Regression tests for filters, pipelines,
and the Connect API passed after that migration. The full verification record
is maintained in [verification evidence](verification.md).

Retaining this SDK does not suppress the scanner output or establish that the
application and its runtime are free of vulnerabilities. Revisit the assessment
if the Go vulnerability database supplies narrower package/symbol information,
a client-specific advisory appears, or the Docker SDK migration is undertaken.

## Browser dependencies

`npm audit --prefix ui --json` on 2026-10-16 reports **three moderate findings**
following the integration of the official `@grafana/ui` and `@grafana/data`
13.2.4 packages. There are no high or critical findings in that audit. The
three entries represent one dependency chain, rather than three independent
application defects:

```text
@grafana/ui@13.2.4
└── react-router-dom-v5-compat
    └── react-router@6.x
```

The underlying advisories are
[GHSA-wrjc-x8rr-h8h6](https://github.com/advisories/GHSA-wrjc-x8rr-h8h6)
(open redirects through React Router links/navigation) and
[GHSA-337j-9hxr-rhxg](https://github.com/advisories/GHSA-337j-9hxr-rhxg)
(constructor injection during React Router server-rendered error hydration).
The audit also marks the compat package and Grafana UI as affected dependents.

GoFi uses Grafana's theme, form controls, buttons, badges, and panels. It does
not use the compatibility router, React Router links/navigation, or
server-rendered React hydration. A production-bundle inspection found no
`deserializeErrors`, `UNSAFE_DataRouter`, or `react-router` code identifiers.
This supports a reduced applicability assessment for these reported paths;
it does not erase the dependency findings or prove all possible runtime paths
safe.

The installed Grafana version supports React 19. The audit's proposed automatic
remediation is a breaking downgrade to Grafana UI 11.2.10. No such downgrade or
incompatible override of the router's major version was applied. Keep the
pinned Grafana release and lockfile, repeat the audit when upstream releases a
compatible fix, and reassess if GoFi starts using router or SSR APIs. This is
**not a zero-finding npm audit**.
