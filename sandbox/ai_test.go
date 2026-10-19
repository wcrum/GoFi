package sandbox

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func aiHook(proxy string, ok bool) AIEnvironment {
	return func() (map[string]string, []EgressRule, bool) {
		if !ok {
			return nil, nil, false
		}
		env := map[string]string{AIEnvProxyURL: proxy, AIEnvProvider: "openai", AIEnvModel: "gpt-4o-mini"}
		return env, []EgressRule{{Namespace: "gofi", PodSelector: map[string]string{"app.kubernetes.io/name": "gofi"}, Port: 9090}}, true
	}
}

func TestAILibraryInheritsTheProxyAndRestartsWhenItChanges(t *testing.T) {
	ctx := context.Background()
	b := &fakeBackend{}
	s := New(b, &FileStore{Path: filepath.Join(t.TempDir(), "libraries.yaml")})
	s.AIEnvironment = aiHook("http://controller:9090/gofi/llm", true)
	plain := Definition{Name: "plain", SourceCode: "import gofi"}
	ai := Definition{Name: "ai", SourceCode: "import gofi", AI: true, Egress: []EgressRule{{CIDR: "10.0.0.7", Port: 4222}}}
	for _, d := range []Definition{plain, ai} {
		if err := s.Apply(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if b.last.Name != "ai" {
		t.Fatalf("last started %q", b.last.Name)
	}
	started := b.last
	if started.Env[AIEnvProxyURL] != "http://controller:9090/gofi/llm" || started.Env[AIEnvProvider] != "openai" || started.Env[AIEnvModel] != "gpt-4o-mini" {
		t.Fatalf("AI env: %v", started.Env)
	}
	for _, key := range []string{"GOFI_LLM_API_KEY", "GOFI_LLM_BASE_URL"} {
		if _, leaked := started.Env[key]; leaked {
			t.Fatalf("%s must never reach a worker", key)
		}
	}
	if len(started.Egress) != 2 || started.Egress[0].CIDR != "10.0.0.7/32" || started.Egress[1].PodSelector["app.kubernetes.io/name"] != "gofi" || started.Egress[1].Port != 9090 || started.Egress[1].Description != AIEgressReason {
		t.Fatalf("AI egress: %+v", started.Egress)
	}
	// The stored definition keeps only the library's own rules and the AI flag.
	stored, _ := s.Get("ai")
	if len(stored.Egress) != 1 || !stored.AI || stored.Env[AIEnvProxyURL] == "" {
		t.Fatalf("stored: %+v", stored)
	}
	if st, _ := s.Status("ai"); !st.AIAvailable {
		t.Fatal("status must report the AI endpoint as available")
	}
	if err := s.Reconcile(ctx); err != nil || b.starts != 2 {
		t.Fatalf("unchanged AI env must not restart: starts=%d err=%v", b.starts, err)
	}
	// The switch is deployment-wide: a changed proxy restarts every library.
	s.AIEnvironment = aiHook("http://controller-2:9090/gofi/llm", true)
	if err := s.Reconcile(ctx); err != nil || b.starts != 4 {
		t.Fatalf("a changed proxy must restart both libraries: starts=%d err=%v", b.starts, err)
	}
	s.AIEnvironment = aiHook("", false)
	if err := s.Reconcile(ctx); err != nil || b.starts != 6 || len(b.last.Env) != 0 {
		t.Fatalf("a withdrawn endpoint restarts the libraries without env: starts=%d env=%v", b.starts, b.last.Env)
	}
	if st, _ := s.Status("ai"); st.AIAvailable {
		t.Fatal("status must report the AI endpoint as unavailable")
	}
	// The per-library flag no longer matters: a library that never set it
	// carries the environment while the deployment offers it.
	s.AIEnvironment = aiHook("http://controller:9090/gofi/llm", true)
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(ctx, Definition{Name: "plain", SourceCode: "import gofi\n# edited"}); err != nil || b.last.Env[AIEnvProxyURL] == "" || b.last.Name != "plain" {
		t.Fatalf("plain library must get the env: %v (%q, %v)", b.last.Env, b.last.Name, err)
	}
}

func TestLibraryByTokenMatchesSavedAndScratchWorkers(t *testing.T) {
	ctx := context.Background()
	b := &fakeBackend{}
	s := New(b, nil)
	s.AIEnvironment = aiHook("http://controller:9090/gofi/llm", true)
	if err := s.Apply(ctx, Definition{Name: "ai", SourceCode: "import gofi", AI: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(ctx, Definition{Name: "plain", SourceCode: "import gofi"}); err != nil {
		t.Fatal(err)
	}
	bindings, _ := s.Bindings(ctx)
	if d, ok := s.LibraryByToken(bindings["ai"].Token); !ok || d.Name != "ai" || !d.AI {
		t.Fatalf("ai token: %+v %v", d, ok)
	}
	if d, ok := s.LibraryByToken(bindings["plain"].Token); !ok || d.Name != "plain" || d.AI {
		t.Fatalf("plain token: %+v %v", d, ok)
	}
	for _, bad := range []string{"", bindings["ai"].Token[:63], bindings["ai"].Token + "0", strings.ToUpper(bindings["ai"].Token)} {
		if _, ok := s.LibraryByToken(bad); ok {
			t.Fatalf("token %q accepted", bad)
		}
	}
	var scratchToken string
	err := s.RunScratch(ctx, Definition{SourceCode: "import gofi", AI: true}, func(_ context.Context, binding Binding) error {
		scratchToken = binding.Token
		d, ok := s.LibraryByToken(binding.Token)
		if !ok || !d.AI || !d.Scratch || d.Env[AIEnvProxyURL] == "" {
			t.Fatalf("scratch token during the run: %+v %v", d, ok)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LibraryByToken(scratchToken); ok {
		t.Fatal("a finished scratch run's token must no longer authenticate")
	}
}

func TestKubernetesAIWorkerCarriesPlainEnvAndControllerEgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := fake.NewClientset()
	c.PrependReactor("create", "pods", func(ktesting.Action) (bool, runtime.Object, error) { cancel(); return false, nil, nil })
	k := &Kubernetes{Client: c, Namespace: "gofi", Image: DefaultImage}
	d := Definition{Name: "ai", SourceCode: "import gofi", TimeoutSeconds: 5, MemoryMiB: 128, AI: true,
		Env:    map[string]string{AIEnvProxyURL: "http://gofi.gofi.svc.cluster.local:9090/gofi/llm", AIEnvProvider: "anthropic", AIEnvModel: "claude-haiku-4-5"},
		Egress: []EgressRule{{Namespace: "gofi", PodSelector: map[string]string{"app.kubernetes.io/name": "gofi"}, Port: 9090, Protocol: "tcp", Description: AIEgressReason}}}
	_, _ = k.Start(ctx, d, "secret")
	pod, err := c.CoreV1().Pods("gofi").Get(context.Background(), "gofi-python-ai", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, e := range pod.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	if env[AIEnvProxyURL] != d.Env[AIEnvProxyURL] || env[AIEnvProvider] != "anthropic" || env[AIEnvModel] != "claude-haiku-4-5" {
		t.Fatalf("env: %v", env)
	}
	secret, _ := c.CoreV1().Secrets("gofi").Get(context.Background(), "gofi-python-ai", metav1.GetOptions{})
	if len(secret.StringData) != 1 {
		t.Fatalf("the worker Secret holds only the token: %v", secret.StringData)
	}
	policy, _ := c.NetworkingV1().NetworkPolicies("gofi").Get(context.Background(), "gofi-python-ai", metav1.GetOptions{})
	if len(policy.Spec.Egress) != 1 { // the controller rule only: the proxy is reached by IP, so no DNS
		t.Fatalf("egress: %+v", policy.Spec.Egress)
	}
	peer := policy.Spec.Egress[0].To[0]
	if peer.PodSelector.MatchLabels["app.kubernetes.io/name"] != "gofi" || peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "gofi" || policy.Spec.Egress[0].Ports[0].Port.IntValue() != 9090 {
		t.Fatalf("controller rule: %+v", policy.Spec.Egress[0])
	}
	// The hash follows the AI environment, not the derived egress rule.
	same := d
	same.Egress = nil
	if definitionHash(d, DefaultImage) != definitionHash(same, DefaultImage) {
		t.Fatal("the derived AI egress rule must not change the definition hash")
	}
	changed := d
	changed.Env = map[string]string{AIEnvProxyURL: d.Env[AIEnvProxyURL], AIEnvProvider: "anthropic", AIEnvModel: "claude-sonnet-4-5"}
	if definitionHash(d, DefaultImage) == definitionHash(changed, DefaultImage) {
		t.Fatal("a changed model must change the definition hash")
	}
	// The deprecated per-library flag is ignored: flipping it alone must not
	// restart a worker; withdrawing the environment must.
	flagged := d
	flagged.AI = false
	if definitionHash(d, DefaultImage) != definitionHash(flagged, DefaultImage) {
		t.Fatal("the deprecated AI flag must not change the definition hash")
	}
	plain := d
	plain.Env = nil
	if definitionHash(d, DefaultImage) == definitionHash(plain, DefaultImage) {
		t.Fatal("the AI environment must be part of the definition hash")
	}
}

func TestKubernetesControllerAddress(t *testing.T) {
	t.Setenv("HOSTNAME", "gofi-7d9f")
	t.Setenv("GOFI_LLM_PROXY_HOST", "")
	self := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "gofi-7d9f", Namespace: "gofi", Labels: map[string]string{"app.kubernetes.io/name": "gofi", "app.kubernetes.io/instance": "rel", "app.kubernetes.io/component": "controller", "pod-template-hash": "x"}},
		Spec: core.PodSpec{Containers: []core.Container{{Name: "gofi", Ports: []core.ContainerPort{{Name: "http", ContainerPort: 9090}, {Name: "llm", ContainerPort: 9091}}}}}}
	other := &core.Service{ObjectMeta: metav1.ObjectMeta{Name: "nats", Namespace: "gofi"}, Spec: core.ServiceSpec{Selector: map[string]string{"app": "nats"}, Ports: []core.ServicePort{{Port: 9091}}}}
	// The chart's Service: the API on 80 -> http and the proxy on 9091 -> llm (named target ports).
	svc := &core.Service{ObjectMeta: metav1.ObjectMeta{Name: "rel-gofi", Namespace: "gofi"}, Spec: core.ServiceSpec{Selector: map[string]string{"app.kubernetes.io/name": "gofi", "app.kubernetes.io/instance": "rel", "app.kubernetes.io/component": "controller"},
		Ports: []core.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromString("http")}, {Name: "llm", Port: 9091, TargetPort: intstr.FromString("llm")}}}}
	k := &Kubernetes{Client: fake.NewClientset(self, other, svc), Namespace: "gofi"}
	addr, rules, err := k.ControllerAddress(context.Background(), 9091)
	if err != nil || addr != "rel-gofi.gofi.svc.cluster.local:9091" {
		t.Fatalf("discovered %q %v", addr, err)
	}
	// The rule selects the controller Pods by their own app.kubernetes.io
	// labels (never the ReplicaSet hash) on the proxy port only.
	want := map[string]string{"app.kubernetes.io/name": "gofi", "app.kubernetes.io/instance": "rel", "app.kubernetes.io/component": "controller"}
	if len(rules) != 1 || rules[0].Namespace != "gofi" || rules[0].Port != 9091 || rules[0].Protocol != "tcp" || len(rules[0].PodSelector) != len(want) {
		t.Fatalf("rules: %+v", rules)
	}
	for key, value := range want {
		if rules[0].PodSelector[key] != value {
			t.Fatalf("selector %v", rules[0].PodSelector)
		}
	}
	// A proxy port no Service port targets is an error, not a guess.
	if _, _, err = k.ControllerAddress(context.Background(), 9093); err == nil {
		t.Fatal("a port the Service does not target must not be used")
	}
	t.Setenv("GOFI_LLM_PROXY_HOST", "gofi-ai.gofi.svc:9091")
	if addr, rules, err = k.ControllerAddress(context.Background(), 9091); err != nil || addr != "gofi-ai.gofi.svc:9091" || len(rules) != 1 {
		t.Fatalf("pinned %q %v %v", addr, rules, err)
	}
	t.Setenv("GOFI_LLM_PROXY_HOST", "")
	k = &Kubernetes{Client: fake.NewClientset(self, other), Namespace: "gofi"}
	if _, _, err = k.ControllerAddress(context.Background(), 9091); err == nil {
		t.Fatal("no matching Service must be an error")
	}
	k = &Kubernetes{Client: fake.NewClientset(other, svc), Namespace: "gofi"}
	if _, _, err = k.ControllerAddress(context.Background(), 9091); err == nil {
		t.Fatal("an unknown controller Pod must be an error (no labels to select by)")
	}
	// Without app.kubernetes.io labels every label but the hashes selects.
	if got := controllerSelector(map[string]string{"app": "gofi", "pod-template-hash": "x", "controller-revision-hash": "y"}); len(got) != 1 || got["app"] != "gofi" {
		t.Fatalf("fallback selector: %v", got)
	}
}
