// Package sandbox manages Python libraries in isolated containers or Pods.
package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

const DefaultImage = "ghcr.io/wcrum/gofi-python:0.2.0"

type Definition struct {
	Name           string `json:"name,omitempty"`
	SourceCode     string `json:"sourceCode"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	MemoryMiB      int    `json:"memoryMib,omitempty"`
	// Tests are saved sample inputs; they never affect the running worker.
	Tests []FunctionTest `json:"tests,omitempty"`
	// ManagedBy is empty for user libraries and "flow:<flow>" for
	// libraries generated from a listener's inline stages (read-only to users).
	ManagedBy string `json:"managedBy,omitempty"`
	// Egress lists permitted outbound destinations; empty means no egress.
	Egress []EgressRule `json:"egress,omitempty"`
	// Scratch marks a short-lived RunSource worker: source is loaded per
	// invocation so load errors are reported instead of failing startup.
	Scratch bool `json:"-"`
}

// FunctionTest is a saved sample input for one library function.
type FunctionTest struct {
	Name         string         `json:"name,omitempty"`
	Function     string         `json:"function,omitempty"`
	Kind         string         `json:"kind,omitempty"`
	Raw          []byte         `json:"raw,omitempty"`
	Fields       map[string]any `json:"fields,omitempty"`
	Meta         map[string]any `json:"meta,omitempty"`
	Config       map[string]any `json:"config,omitempty"`
	ExpectFields map[string]any `json:"expectFields,omitempty"`
	ExpectDrop   *bool          `json:"expectDrop,omitempty"`
}

// EgressRule permits outbound traffic from a library worker. Kubernetes
// enforces each rule precisely; Docker can only grant or deny all egress.
type EgressRule struct {
	CIDR        string            `json:"cidr,omitempty"`
	Port        int               `json:"port,omitempty"`
	Protocol    string            `json:"protocol,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	PodSelector map[string]string `json:"podSelector,omitempty"`
	Description string            `json:"description,omitempty"`
}

const (
	maxTests       = 64
	maxEgressRules = 32
	// ManagedPrefix and ScratchPrefix are reserved library-name prefixes.
	ManagedPrefix = "flow-"
	ScratchPrefix = "run-"
)

// Managed reports whether the controller owns this library.
func (d Definition) Managed() bool { return d.ManagedBy != "" }

// runtimeEqual compares only what the running worker depends on.
func runtimeEqual(a, b Definition) bool {
	return a.Name == b.Name && a.SourceCode == b.SourceCode && a.TimeoutSeconds == b.TimeoutSeconds && a.MemoryMiB == b.MemoryMiB &&
		slices.EqualFunc(a.Egress, b.Egress, func(x, y EgressRule) bool {
			return x.CIDR == y.CIDR && x.Port == y.Port && x.Protocol == y.Protocol && x.Namespace == y.Namespace && maps.Equal(x.PodSelector, y.PodSelector)
		})
}

func normalizeEgress(rules []EgressRule) ([]EgressRule, error) {
	if len(rules) > maxEgressRules {
		return nil, fmt.Errorf("at most %d egress rules are allowed", maxEgressRules)
	}
	out := make([]EgressRule, 0, len(rules))
	for i, r := range rules {
		where := fmt.Sprintf("egress[%d]", i)
		if r.CIDR == "" && r.Namespace == "" && len(r.PodSelector) == 0 {
			return nil, fmt.Errorf("%s: cidr or namespace/podSelector is required", where)
		}
		if r.CIDR != "" {
			if ip := net.ParseIP(r.CIDR); ip != nil {
				if ip.To4() != nil {
					r.CIDR += "/32"
				} else {
					r.CIDR += "/128"
				}
			}
			if _, network, err := net.ParseCIDR(r.CIDR); err != nil {
				return nil, fmt.Errorf("%s: invalid cidr %q", where, r.CIDR)
			} else {
				r.CIDR = network.String()
			}
		}
		if r.Port < 0 || r.Port > 65535 {
			return nil, fmt.Errorf("%s: port must be 0 (any) or 1-65535", where)
		}
		r.Protocol = strings.ToLower(r.Protocol)
		if r.Protocol == "" {
			r.Protocol = "tcp"
		}
		if r.Protocol != "tcp" && r.Protocol != "udp" {
			return nil, fmt.Errorf("%s: protocol must be tcp or udp", where)
		}
		if r.Namespace != "" {
			if errs := validation.IsDNS1123Label(r.Namespace); len(errs) > 0 {
				return nil, fmt.Errorf("%s: invalid namespace: %s", where, strings.Join(errs, "; "))
			}
		}
		for key, value := range r.PodSelector {
			if errs := validation.IsQualifiedName(key); len(errs) > 0 {
				return nil, fmt.Errorf("%s: invalid podSelector key %q", where, key)
			}
			if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
				return nil, fmt.Errorf("%s: invalid podSelector value for %q", where, key)
			}
		}
		if len(r.Description) > 200 {
			return nil, fmt.Errorf("%s: description exceeds 200 characters", where)
		}
		out = append(out, r)
	}
	return out, nil
}

func normalizeTests(tests []FunctionTest) error {
	if len(tests) > maxTests {
		return fmt.Errorf("at most %d saved tests are allowed", maxTests)
	}
	for i, t := range tests {
		switch t.Kind {
		case "listener", "parser", "transformer", "sink":
		default:
			return fmt.Errorf("tests[%d]: kind must be listener, parser, transformer, or sink", i)
		}
		if t.Function == "" || len(t.Function) > 128 || len(t.Name) > 128 {
			return fmt.Errorf("tests[%d]: function is required and names are limited to 128 characters", i)
		}
		if len(t.Raw) > 1<<20 {
			return fmt.Errorf("tests[%d]: raw sample exceeds 1 MiB", i)
		}
	}
	return nil
}

type Binding struct {
	Endpoint       string `json:"endpoint"`
	Token          string `json:"token"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}
type Status struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
	Error string `json:"error,omitempty"`
}
type Store interface {
	Load(context.Context) ([]Definition, error)
	Save(context.Context, Definition) error
	Delete(context.Context, string) error
}
type HealthBackend interface {
	Health(context.Context, Binding) error
}

type Backend interface {
	Start(context.Context, Definition, string) (string, error)
	Stop(context.Context, string) error
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

func Normalize(d Definition) (Definition, error) {
	if !namePattern.MatchString(d.Name) {
		return d, fmt.Errorf("library name must be 1-40 lowercase letters, numbers, or hyphens, starting with a letter")
	}
	if len(d.SourceCode) == 0 || len(d.SourceCode) > 65536 {
		return d, fmt.Errorf("sourceCode must contain 1-65536 bytes")
	}
	if d.TimeoutSeconds == 0 {
		d.TimeoutSeconds = 5
	}
	if d.TimeoutSeconds < 1 || d.TimeoutSeconds > 30 {
		return d, fmt.Errorf("timeoutSeconds must be between 1 and 30")
	}
	if d.MemoryMiB == 0 {
		d.MemoryMiB = 128
	}
	if d.MemoryMiB < 64 || d.MemoryMiB > 1024 {
		return d, fmt.Errorf("memoryMib must be between 64 and 1024")
	}
	if d.ManagedBy != "" && (!strings.HasPrefix(d.ManagedBy, "flow:") || len(d.ManagedBy) > 260) {
		return d, fmt.Errorf("managedBy must be empty or flow:<flow>")
	}
	if err := normalizeTests(d.Tests); err != nil {
		return d, err
	}
	egress, err := normalizeEgress(d.Egress)
	if err != nil {
		return d, err
	}
	d.Egress = egress
	if len(d.Egress) == 0 {
		d.Egress = nil
	}
	return d, nil
}

// NormalizeUser additionally reserves the system name prefixes and the
// managed marker for libraries the controller generates.
func NormalizeUser(d Definition) (Definition, error) {
	if strings.HasPrefix(d.Name, ManagedPrefix) || strings.HasPrefix(d.Name, ScratchPrefix) {
		return d, fmt.Errorf("library names starting with %q or %q are reserved for GoFi", ManagedPrefix, ScratchPrefix)
	}
	if d.ManagedBy != "" {
		return d, fmt.Errorf("managedBy is set by GoFi for flow-managed libraries")
	}
	return Normalize(d)
}

type Supervisor struct {
	mu             sync.RWMutex
	operations     sync.Mutex
	backend        Backend
	store          Store
	defs           map[string]Definition
	bindings       map[string]Binding
	statuses       map[string]Status
	toolingMu      sync.Mutex
	toolingName    string
	tooling        Binding
	toolingClosed  bool
	toolingContext context.Context
	toolingCancel  context.CancelFunc
	// scratch maps active RunSource worker names to their tokens (for log
	// redaction); entries exist from before Start until after Stop.
	scratch map[string]string
	// healthFailures counts consecutive failed health probes per library;
	// a worker restarts only after unhealthyThreshold failures in a row.
	healthFailures map[string]int
}

func New(b Backend, s Store) *Supervisor {
	toolingContext, toolingCancel := context.WithCancel(context.Background())
	return &Supervisor{backend: b, store: s, defs: map[string]Definition{}, bindings: map[string]Binding{}, statuses: map[string]Status{}, toolingContext: toolingContext, toolingCancel: toolingCancel, scratch: map[string]string{}}
}
func (s *Supervisor) Apply(ctx context.Context, d Definition) error {
	d, err := Normalize(d)
	if err != nil {
		return err
	}
	s.operations.Lock()
	defer s.operations.Unlock()
	if s.store != nil {
		if err = s.store.Save(ctx, d); err != nil {
			return err
		}
	}
	if old, ok := s.Get(d.Name); ok && runtimeEqual(old, d) {
		if st, _ := s.Status(d.Name); st.Phase == "Ready" {
			s.mu.Lock()
			s.defs[d.Name] = d
			s.mu.Unlock()
			return nil
		}
	}
	return s.start(ctx, d)
}
func (s *Supervisor) start(ctx context.Context, d Definition) error {
	if s.backend == nil {
		return fmt.Errorf("Python sandbox backend is not configured")
	}
	s.mu.Lock()
	s.defs[d.Name] = d
	s.statuses[d.Name] = Status{Name: d.Name, Phase: "Starting"}
	delete(s.bindings, d.Name)
	s.mu.Unlock()
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	value := hex.EncodeToString(token)
	endpoint, err := s.backend.Start(ctx, d, value)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.statuses[d.Name] = Status{Name: d.Name, Phase: "Failed", Error: err.Error()}
		return err
	}
	s.bindings[d.Name] = Binding{Endpoint: endpoint, Token: value, TimeoutSeconds: d.TimeoutSeconds}
	s.statuses[d.Name] = Status{Name: d.Name, Phase: "Ready"}
	return nil
}
func (s *Supervisor) Delete(ctx context.Context, name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid library name")
	}
	s.operations.Lock()
	defer s.operations.Unlock()
	if s.store != nil {
		if err := s.store.Delete(ctx, name); err != nil {
			return err
		}
	}
	if s.backend != nil {
		if err := s.backend.Stop(ctx, name); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.defs, name)
	delete(s.bindings, name)
	delete(s.statuses, name)
	return nil
}
func (s *Supervisor) List() []Definition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Definition, 0, len(s.defs))
	for _, d := range s.defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (s *Supervisor) Get(name string) (Definition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.defs[name]
	return v, ok
}
func (s *Supervisor) Status(name string) (Status, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.statuses[name]
	return v, ok
}
func (s *Supervisor) Bindings(context.Context) (map[string]Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]Binding{}
	for n, b := range s.bindings {
		out[n] = b
	}
	return out, nil
}

// unhealthyThreshold is how many consecutive failed health probes (one per
// 5 s reconcile) restart a worker. One slow probe on a busy host must not
// kill a worker that is in the middle of running functions.
const unhealthyThreshold = 3

// observeHealth records a probe result and reports whether the worker should
// be kept: it stays until unhealthyThreshold consecutive probes fail.
func (s *Supervisor) observeHealth(name string, err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.healthFailures == nil {
		s.healthFailures = map[string]int{}
	}
	if err == nil {
		delete(s.healthFailures, name)
		return true
	}
	s.healthFailures[name]++
	failures := s.healthFailures[name]
	slog.Warn("Python library health check failed", "library", name, "consecutiveFailures", failures, "threshold", unhealthyThreshold, "error", err)
	return failures < unhealthyThreshold
}

func (s *Supervisor) resetHealth(name string) {
	s.mu.Lock()
	delete(s.healthFailures, name)
	s.mu.Unlock()
}

func (s *Supervisor) Reconcile(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	s.operations.Lock()
	defer s.operations.Unlock()
	defs, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, d := range defs {
		d, err = Normalize(d)
		if err != nil {
			return err
		}
		seen[d.Name] = true
		old, ok := s.Get(d.Name)
		st, _ := s.Status(d.Name)
		healthy := true
		if hb, supports := s.backend.(HealthBackend); supports && ok && st.Phase == "Ready" {
			s.mu.RLock()
			binding := s.bindings[d.Name]
			s.mu.RUnlock()
			healthy = s.observeHealth(d.Name, hb.Health(ctx, binding))
		}
		if ok && runtimeEqual(old, d) && st.Phase == "Ready" && healthy {
			// Saved tests and metadata change without restarting the worker.
			s.mu.Lock()
			s.defs[d.Name] = d
			s.mu.Unlock()
			continue
		}
		switch {
		case !ok:
			slog.Info("Python library starting", "library", d.Name)
		case !runtimeEqual(old, d):
			slog.Info("Python library restarting: definition changed", "library", d.Name)
		case st.Phase != "Ready":
			slog.Info("Python library restarting: not ready", "library", d.Name, "phase", st.Phase, "error", st.Error)
		default:
			slog.Warn("Python library restarting: health checks failed", "library", d.Name, "consecutiveFailures", unhealthyThreshold)
		}
		s.resetHealth(d.Name)
		if err = s.start(ctx, d); err != nil {
			slog.Error("Python library start failed", "library", d.Name, "error", err)
		}
	}
	for _, d := range s.List() {
		if !seen[d.Name] {
			if s.backend != nil {
				if err = s.backend.Stop(ctx, d.Name); err != nil {
					return err
				}
			}
			s.mu.Lock()
			delete(s.defs, d.Name)
			delete(s.bindings, d.Name)
			delete(s.statuses, d.Name)
			s.mu.Unlock()
		}
	}
	return nil
}
func (s *Supervisor) Run(ctx context.Context) error {
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.CloseTooling(cleanup); err != nil {
			slog.Warn("Python tooling cleanup failed", "error", err)
		}
	}()
	if err := s.Reconcile(ctx); err != nil {
		return err
	}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := s.Reconcile(ctx); err != nil {
				slog.Error("Python library reconciliation failed", "error", err)
			}
		}
	}
}
