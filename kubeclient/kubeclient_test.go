package kubeclient

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestConfigureAppliesRateLimitTimeoutAndUserAgent(t *testing.T) {
	t.Setenv("GOFI_KUBE_QPS", "7.5")
	t.Setenv("GOFI_KUBE_BURST", "9")
	t.Setenv("GOFI_KUBE_TIMEOUT", "3s")
	cfg := Configure(&rest.Config{Host: "https://example"})
	if cfg.QPS != 7.5 || cfg.Burst != 9 || cfg.Timeout != 3*time.Second || cfg.UserAgent != UserAgent {
		t.Fatalf("config not applied: %+v", cfg)
	}
	t.Setenv("GOFI_KUBE_QPS", "nonsense")
	t.Setenv("GOFI_KUBE_BURST", "")
	t.Setenv("GOFI_KUBE_TIMEOUT", "")
	cfg = Configure(&rest.Config{})
	if cfg.QPS != DefaultQPS || cfg.Burst != DefaultBurst || cfg.Timeout != DefaultTimeout {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	clients, err := NewClients(&rest.Config{Host: "https://example"})
	if err != nil {
		t.Fatal(err)
	}
	if clients.WatchConfig.Timeout != 0 || clients.Config.Timeout == 0 {
		t.Fatal("watch config must not carry the request timeout")
	}
}

// ListAll follows continue tokens and stops at the limit with truncated set.
func TestListAllPaginatesAndTruncates(t *testing.T) {
	pages := map[string][]int{"": {1, 2, 3}, "p2": {4, 5, 6}, "p3": {7, 8}}
	next := map[string]string{"": "p2", "p2": "p3", "p3": ""}
	calls := 0
	page := func(_ context.Context, opts metav1.ListOptions) ([]int, string, error) {
		calls++
		if opts.Limit <= 0 {
			return nil, "", fmt.Errorf("page size missing")
		}
		return pages[opts.Continue], next[opts.Continue], nil
	}
	items, truncated, err := ListAll(context.Background(), 0, "", page)
	if err != nil || truncated || len(items) != 8 || calls != 3 {
		t.Fatalf("full listing: %v %v %d calls=%d", items, truncated, len(items), calls)
	}
	calls = 0
	items, truncated, err = ListAll(context.Background(), 5, "", page)
	if err != nil || !truncated || len(items) != 5 || calls != 2 {
		t.Fatalf("truncated listing: %v %v %d calls=%d", items, truncated, len(items), calls)
	}
	if TruncationWarning("Pods", 5) != "Pods: more than 5 objects; showing the first 5" {
		t.Fatal(TruncationWarning("Pods", 5))
	}
}

// Two replicas compete for the Lease; when the leader stops, the standby
// takes over within the lease duration.
func TestLeaderElectionHandsOverWhenLeaderStops(t *testing.T) {
	cs := fake.NewClientset()
	cfg := func(identity string) ElectionConfig {
		// Lease records carry second-precision times, so durations stay in
		// whole seconds (as in production: 15s / 10s / 2s).
		return ElectionConfig{Enabled: true, LeaseName: "gofi-test", Identity: identity, LeaseDuration: 3 * time.Second, RenewDeadline: 2 * time.Second, RetryPeriod: 500 * time.Millisecond}
	}
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	leadA := make(chan struct{}, 1)
	a, err := RunElection(ctxA, cs, "gofi", cfg("replica-a"), LeaderCallbacks{OnStartedLeading: func(context.Context) { leadA <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-leadA:
	case <-time.After(5 * time.Second):
		t.Fatal("replica-a never led")
	}
	leadB := make(chan struct{}, 1)
	b, err := RunElection(ctxB, cs, "gofi", cfg("replica-b"), LeaderCallbacks{OnStartedLeading: func(context.Context) { leadB <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if !a.IsLeader() || b.IsLeader() {
		t.Fatalf("one leader expected: a=%v b=%v", a.IsLeader(), b.IsLeader())
	}
	if b.Holder() != "replica-a" {
		t.Fatalf("standby sees holder %q", b.Holder())
	}
	stopped := time.Now()
	cancelA() // releases the lease (ReleaseOnCancel)
	select {
	case <-leadB:
	case <-time.After(10 * time.Second):
		t.Fatal("replica-b never took over")
	}
	if a.IsLeader() {
		t.Fatal("replica-a still leads after stopping")
	}
	// Released on cancel, so the standby acquires on its next retry, well
	// inside the lease duration it would otherwise have to wait out.
	if took := time.Since(stopped); took > 2*time.Second {
		t.Fatalf("handover took %s", took)
	}
	if _, err := cs.CoordinationV1().Leases("gofi").Get(context.Background(), "gofi-test", metav1.GetOptions{}); err != nil {
		t.Fatalf("lease object: %v", err)
	}
	// Invalid timing is refused rather than campaigning with a broken lease.
	bad := cfg("replica-c")
	bad.RenewDeadline = bad.LeaseDuration
	if _, err := RunElection(context.Background(), cs, "gofi", bad, LeaderCallbacks{}); err == nil {
		t.Fatal("renewDeadline >= leaseDuration accepted")
	}
}

func TestPodDefaultsFromEnvironment(t *testing.T) {
	t.Setenv("GOFI_WORKER_RESOURCES", `{"requests":{"cpu":"50m"},"limits":{"cpu":"1","memory":"256Mi"}}`)
	t.Setenv("GOFI_WORKER_SCHEDULING", `{"priorityClassName":"gofi-dataplane","nodeSelector":{"pool":"data"},"terminationGracePeriodSeconds":10}`)
	t.Setenv("GOFI_WORKER_PROBES", `{"startup":{"enabled":true,"periodSeconds":1,"failureThreshold":60},"readiness":{"enabled":true,"periodSeconds":5,"timeoutSeconds":2,"failureThreshold":3},"liveness":{"enabled":false}}`)
	defaults, err := LoadPodDefaults("WORKER")
	if err != nil {
		t.Fatal(err)
	}
	resources := defaults.ResourcesOr(corev1.ResourceRequirements{})
	if resources.Limits.Memory().String() != "256Mi" || resources.Requests.Cpu().String() != "50m" {
		t.Fatalf("resources: %+v", resources)
	}
	if GoMemLimit(resources.Limits) != fmt.Sprint(256*1024*1024/10*9) {
		t.Fatalf("GOMEMLIMIT %q", GoMemLimit(resources.Limits))
	}
	if GoMemLimit(nil) != "" {
		t.Fatal("GOMEMLIMIT without a memory limit")
	}
	var spec corev1.PodSpec
	defaults.ApplyScheduling(&spec)
	if spec.PriorityClassName != "gofi-dataplane" || spec.NodeSelector["pool"] != "data" || spec.TerminationGracePeriodSeconds == nil || *spec.TerminationGracePeriodSeconds != 10 {
		t.Fatalf("scheduling: %+v", spec)
	}
	var container corev1.Container
	defaults.ApplyProbes(&container, 8080, "/healthz")
	if container.StartupProbe == nil || container.StartupProbe.FailureThreshold != 60 || container.ReadinessProbe == nil || container.ReadinessProbe.HTTPGet.Port.IntValue() != 8080 || container.LivenessProbe != nil {
		t.Fatalf("probes: startup=%+v readiness=%+v liveness=%+v", container.StartupProbe, container.ReadinessProbe, container.LivenessProbe)
	}
	t.Setenv("GOFI_WORKER_RESOURCES", "not json")
	if _, err := LoadPodDefaults("WORKER"); err == nil {
		t.Fatal("malformed resources accepted")
	}
}

// GOFI_<PREFIX>_ENV reaches the main container without overriding what the
// driver already set.
func TestPodDefaultsExtraEnv(t *testing.T) {
	t.Setenv("GOFI_LISTENER_ENV", `[{"name":"GOFI_PROFILE","value":"edge"},{"name":"FLOW_SPEC","value":"evil"},{"name":"FROM_SECRET","valueFrom":{"secretKeyRef":{"name":"s","key":"k"}}}]`)
	defaults, err := LoadPodDefaults("LISTENER")
	if err != nil {
		t.Fatal(err)
	}
	container := corev1.Container{Env: []corev1.EnvVar{{Name: "FLOW_SPEC", Value: "{}"}}}
	defaults.ApplyEnv(&container)
	if len(container.Env) != 3 {
		t.Fatalf("env = %+v", container.Env)
	}
	if container.Env[0].Value != "{}" {
		t.Fatalf("driver-owned FLOW_SPEC overridden: %+v", container.Env[0])
	}
	if container.Env[1].Name != "GOFI_PROFILE" || container.Env[1].Value != "edge" {
		t.Fatalf("GOFI_PROFILE missing: %+v", container.Env)
	}
	if container.Env[2].ValueFrom == nil || container.Env[2].ValueFrom.SecretKeyRef == nil || container.Env[2].ValueFrom.SecretKeyRef.Name != "s" {
		t.Fatalf("valueFrom lost: %+v", container.Env[2])
	}
	for _, bad := range []string{`not json`, `[{"value":"x"}]`, `[{"name":"A"},{"name":"A"}]`} {
		t.Setenv("GOFI_LISTENER_ENV", bad)
		if _, err := LoadPodDefaults("LISTENER"); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	t.Setenv("GOFI_LISTENER_ENV", "[]")
	if defaults, err := LoadPodDefaults("LISTENER"); err != nil || len(defaults.Env) != 0 {
		t.Fatalf("empty list: %v %+v", err, defaults.Env)
	}
}
