package kubeclient

import "testing"

func TestWorkerEnvRejectsLLMCredentials(t *testing.T) {
	t.Setenv("GOFI_WORKER_ENV", `[{"name":"OPENAI_API_KEY","value":"sk"}]`)
	if _, err := LoadPodDefaults("WORKER"); err == nil {
		t.Fatal("OPENAI_API_KEY accepted for workers")
	}
	t.Setenv("GOFI_WORKER_ENV", `[{"name":"LANGSMITH_TRACING","value":"true"}]`)
	if _, err := LoadPodDefaults("WORKER"); err == nil {
		t.Fatal("LANGSMITH_TRACING accepted for workers")
	}
	t.Setenv("GOFI_WORKER_ENV", `[{"name":"TZ","value":"UTC"}]`)
	if _, err := LoadPodDefaults("WORKER"); err != nil {
		t.Fatalf("harmless variable rejected: %v", err)
	}
	// Listener pods run GoFi's own runtime, not user code: no restriction.
	t.Setenv("GOFI_LISTENER_ENV", `[{"name":"OPENAI_API_KEY","value":"sk"}]`)
	if _, err := LoadPodDefaults("LISTENER"); err != nil {
		t.Fatalf("listener env rejected: %v", err)
	}
}
