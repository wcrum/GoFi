// Package e2e holds chaos and failure scenarios against a real GoFi
// deployment: the binary (in-process runtimes), the Docker/Compose shape
// (controller container driving sibling listener containers), or a
// Kubernetes cluster (k3d). Each scenario drives identified traffic through a
// flow into an HTTP receiver, injects a failure, asserts the invariants
// (internal/testkit/invariants), and writes a JSON report under
// testenv/reports/. Built only with the "e2e" tag:
//
//	task test:e2e                       # binary shape (default)
//	GOFI_E2E_MODE=compose task test:e2e # Docker shape (needs GOFI_E2E_IMAGE)
//	GOFI_E2E_MODE=kubernetes task test:e2e:k3d
//	task test:chaos                     # binary + edge profile scenarios
//
// See docs/testing.md.
package e2e
