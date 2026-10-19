package sandbox

import (
	"context"
	"errors"
	"testing"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// A healthy worker left by the previous controller is adopted: its token is
// read from the Secret and nothing is recreated, so listeners carrying that
// token stay valid across a controller restart.
func TestKubernetesAdoptsHealthyWorkerFromSecret(t *testing.T) {
	ctx := context.Background()
	def, _ := Normalize(Definition{Name: "library", SourceCode: "import gofi"})
	cs := fake.NewClientset(&core.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gofi-python-library", Namespace: "gofi", Labels: workerLabels("library"), Annotations: map[string]string{annotationDefinitionHash: definitionHash(def, DefaultImage)}},
		Data:       map[string][]byte{"token": []byte("persisted-token")},
	})
	healthy := true
	k := &Kubernetes{Client: cs, Namespace: "gofi", Image: DefaultImage, health: func(_ context.Context, b Binding) error {
		if !healthy {
			return errors.New("connection refused")
		}
		if b.Token != "persisted-token" {
			return errors.New("unauthorized")
		}
		return nil
	}}
	endpoint, token, ok, err := k.Adopt(ctx, def)
	if err != nil || !ok || token != "persisted-token" || endpoint != "http://gofi-python-library.gofi.svc.cluster.local:8080" {
		t.Fatalf("adopt: %q %q %v %v", endpoint, token, ok, err)
	}
	// A changed definition rotates instead of adopting.
	changed := def
	changed.SourceCode = "import gofi\nx = 1"
	if _, _, ok, err := k.Adopt(ctx, changed); err != nil || ok {
		t.Fatalf("changed definition adopted: %v %v", ok, err)
	}
	// An unhealthy worker is recreated.
	healthy = false
	if _, _, ok, err := k.Adopt(ctx, def); err != nil || ok {
		t.Fatalf("unhealthy worker adopted: %v %v", ok, err)
	}
	// Another owner's Secret is never used.
	foreign := fake.NewClientset(&core.Secret{ObjectMeta: metav1.ObjectMeta{Name: "gofi-python-library", Namespace: "gofi"}, Data: map[string][]byte{"token": []byte("x")}})
	k.Client = foreign
	healthy = true
	if _, _, ok, err := k.Adopt(ctx, def); err != nil || ok {
		t.Fatalf("unmanaged Secret adopted: %v %v", ok, err)
	}
}

type adoptingBackend struct {
	fakeBackend
	adopt map[string]string // library -> token
}

func (a *adoptingBackend) Adopt(_ context.Context, d Definition) (string, string, bool, error) {
	token, ok := a.adopt[d.Name]
	return "http://worker:8080", token, ok, nil
}

// The supervisor adopts on its first reconcile instead of starting, and
// starts only libraries without a worker to adopt.
func TestSupervisorAdoptsInsteadOfRestarting(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{defs: []Definition{{Name: "kept", SourceCode: "import gofi"}, {Name: "fresh", SourceCode: "import gofi"}}}
	backend := &adoptingBackend{adopt: map[string]string{"kept": "old-token"}}
	s := New(backend, store)
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if backend.starts != 1 || backend.last.Name != "fresh" {
		t.Fatalf("want one start (fresh), got %d (%s)", backend.starts, backend.last.Name)
	}
	bindings, _ := s.Bindings(ctx)
	if bindings["kept"].Token != "old-token" || bindings["kept"].Endpoint != "http://worker:8080" {
		t.Fatalf("adopted binding not installed: %+v", bindings["kept"])
	}
	if st, _ := s.Status("kept"); st.Phase != "Ready" {
		t.Fatalf("adopted library phase %q", st.Phase)
	}
	// Unchanged state on the next pass keeps both workers.
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if backend.starts != 1 {
		t.Fatal("adopted worker restarted")
	}
}

// A standby supervisor lists libraries without touching workers.
func TestPassiveSupervisorObservesOnly(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{defs: []Definition{{Name: "library", SourceCode: "import gofi"}}}
	backend := &adoptingBackend{adopt: map[string]string{"library": "t"}}
	s := New(backend, store)
	s.SetPassive(true)
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if backend.starts != 0 || len(s.List()) != 1 {
		t.Fatalf("passive reconcile acted: starts=%d list=%d", backend.starts, len(s.List()))
	}
	if st, _ := s.Status("library"); st.Phase != "Standby" {
		t.Fatalf("phase %q", st.Phase)
	}
	if err := s.Apply(ctx, Definition{Name: "library", SourceCode: "x"}); !errors.Is(err, ErrStandby) {
		t.Fatalf("standby accepted a write: %v", err)
	}
	// Becoming leader adopts the running worker.
	s.SetPassive(false)
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if backend.starts != 0 {
		t.Fatal("leader restarted a worker it could adopt")
	}
	if st, _ := s.Status("library"); st.Phase != "Ready" {
		t.Fatalf("phase after takeover %q", st.Phase)
	}
}

type memoryStore struct{ defs []Definition }

func (m *memoryStore) Load(context.Context) ([]Definition, error) { return m.defs, nil }
func (m *memoryStore) Save(_ context.Context, d Definition) error {
	for i := range m.defs {
		if m.defs[i].Name == d.Name {
			m.defs[i] = d
			return nil
		}
	}
	m.defs = append(m.defs, d)
	return nil
}
func (m *memoryStore) Delete(_ context.Context, name string) error {
	kept := m.defs[:0]
	for _, d := range m.defs {
		if d.Name != name {
			kept = append(kept, d)
		}
	}
	m.defs = kept
	return nil
}
