// Package integration holds controller convergence tests that run against a
// real desired-state store (a flow file, or Flow CRDs on an envtest
// kube-apiserver) and an in-memory runtime driver. They are built only with
// the "integration" tag:
//
//	task test:integration
//	GOFI_ENVTEST_DIR=$(setup-envtest use -p path --bin-dir .envtest) go test -tags integration ./tests/integration/...
//
// See docs/testing.md.
package integration
