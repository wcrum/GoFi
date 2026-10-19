// Package report writes one JSON document per scenario under
// testenv/reports/ so chaos, scale, and edge runs leave measurable evidence
// (docs/testing.md explains the fields).
package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Scenario is one scenario's outcome.
type Scenario struct {
	Name       string            `json:"name"`
	Deployment string            `json:"deployment"` // binary | compose | kubernetes | edge
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt time.Time         `json:"finishedAt"`
	Duration   string            `json:"duration"`
	Passed     bool              `json:"passed"`
	Failure    string            `json:"failure,omitempty"`
	Metrics    map[string]any    `json:"metrics"`
	Invariants []any             `json:"invariants"` // violations at the end (empty when converged)
	Steps      []Step            `json:"steps"`
	Host       map[string]string `json:"host"`
	mu         sync.Mutex
}

// Step is a timestamped note inside a scenario.
type Step struct {
	At   time.Time `json:"at"`
	Note string    `json:"note"`
}

// Start opens a scenario.
func Start(name, deployment string) *Scenario {
	host, _ := os.Hostname()
	return &Scenario{Name: name, Deployment: deployment, StartedAt: time.Now(), Metrics: map[string]any{}, Invariants: []any{}, Host: map[string]string{"hostname": host, "os": runtime.GOOS, "arch": runtime.GOARCH, "go": runtime.Version()}}
}

// Note appends a step.
func (s *Scenario) Note(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Steps = append(s.Steps, Step{At: time.Now(), Note: sprintf(format, args...)})
}

// Set records a metric.
func (s *Scenario) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Metrics[key] = value
}

// Finish stamps the result; violations is the final invariant check.
func (s *Scenario) Finish(passed bool, failure string, violations any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FinishedAt = time.Now()
	s.Duration = s.FinishedAt.Sub(s.StartedAt).Round(time.Millisecond).String()
	s.Passed = passed
	s.Failure = failure
	if violations != nil {
		if list, ok := violations.([]any); ok {
			s.Invariants = list
		} else {
			data, _ := json.Marshal(violations)
			var list []any
			_ = json.Unmarshal(data, &list)
			if list != nil {
				s.Invariants = list
			}
		}
	}
}

// Dir is where reports go: GOFI_REPORT_DIR or <repo>/testenv/reports.
func Dir() string {
	if dir := os.Getenv("GOFI_REPORT_DIR"); dir != "" {
		return dir
	}
	root := repoRoot()
	if root == "" {
		return "testenv/reports"
	}
	return filepath.Join(root, "testenv", "reports")
}

// Write saves the scenario as <dir>/<deployment>-<name>.json and returns the
// path.
func (s *Scenario) Write() (string, error) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	s.mu.Lock()
	data, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	name := strings.NewReplacer("/", "-", " ", "_", "#", "").Replace(s.Name)
	path := filepath.Join(dir, s.Deployment+"-"+name+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}

// Table renders metrics as a Markdown table, keys sorted.
func (s *Scenario) Table() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.Metrics))
	for k := range s.Metrics {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := "| metric | value |\n| --- | --- |\n"
	for _, k := range keys {
		out += "| " + k + " | " + sprintf("%v", s.Metrics[k]) + " |\n"
	}
	return out
}

func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
