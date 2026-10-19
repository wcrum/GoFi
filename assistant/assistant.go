// Package assistant is GoFiBot: a chat assistant over a configurable
// Anthropic Messages API endpoint, with server-side tools that read and (when
// allowed) change this deployment's flows, CEL expressions, and Python
// libraries. The controller owns the API key; browsers never see it.
package assistant

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultModel is used when the configuration names none.
const DefaultModel = "claude-opus-5-5"

const (
	DefaultMaxToolSteps = 8
	MaxToolSteps        = 20
	MaxInstructions     = 8192
)

// Configuration is what an administrator sets. It is stored as one YAML
// document (file) or one Secret (Kubernetes); the key is part of it because
// it is the one value that must not be in a plain file.
type Configuration struct {
	Enabled bool `json:"enabled,omitempty"`
	// Provider is the protocol the endpoint speaks: "anthropic" (the
	// Messages API, default) or "openai" (Chat Completions: Groq, vLLM,
	// LiteLLM, Ollama, OpenRouter, ...).
	Provider     string `json:"provider,omitempty"`
	BaseURL      string `json:"baseUrl,omitempty"`
	Model        string `json:"model,omitempty"`
	APIKey       string `json:"apiKey,omitempty"`
	AllowChanges bool   `json:"allowChanges,omitempty"`
	// AllowFunctions lets Python libraries call this endpoint through
	// gofi.llm (the controller's LLM proxy; the key never reaches a
	// worker). One switch for the deployment, decided here rather than
	// per library.
	AllowFunctions bool   `json:"allowFunctions,omitempty"`
	Instructions   string `json:"instructions,omitempty"`
	MaxToolSteps   int    `json:"maxToolSteps,omitempty"`
	Effort         string `json:"effort,omitempty"`
	// ToolApproval is "changes" (default: only apply tools wait, as
	// proposals decided after the reply) or "all" (every tool call pauses
	// the reply until the user approves or denies it in the chat).
	ToolApproval string `json:"toolApproval,omitempty"`
}

// Tool approval modes.
const (
	ApproveChanges = "changes"
	ApproveAll     = "all"
)

func (c Configuration) ToolApprovalOrDefault() string {
	if c.ToolApproval == "" {
		return ApproveChanges
	}
	return c.ToolApproval
}

// Store persists the configuration (file in binary/Docker mode, a Secret in
// Kubernetes), mirroring expressions.Store.
type Store interface {
	Load(context.Context) (Configuration, error)
	Save(context.Context, Configuration) error
	// Backend names where the configuration lives, for the portal.
	Backend() string
}

// Normalize validates and applies defaults; it does not touch the key.
func Normalize(c Configuration) (Configuration, error) {
	c.BaseURL = strings.TrimSpace(c.BaseURL)
	if c.BaseURL != "" {
		u, err := url.Parse(c.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return c, fmt.Errorf("baseUrl must be an http(s) URL")
		}
		if u.User != nil {
			return c, fmt.Errorf("baseUrl must not carry credentials; the API key is a separate field")
		}
	}
	c.Model = strings.TrimSpace(c.Model)
	if len(c.Model) > 128 {
		return c, fmt.Errorf("model exceeds 128 characters")
	}
	if c.MaxToolSteps == 0 {
		c.MaxToolSteps = DefaultMaxToolSteps
	}
	if c.MaxToolSteps < 1 || c.MaxToolSteps > MaxToolSteps {
		return c, fmt.Errorf("maxToolSteps must be between 1 and %d", MaxToolSteps)
	}
	switch c.Provider {
	case "", ProviderAnthropic, ProviderOpenAI:
	default:
		return c, fmt.Errorf("provider must be anthropic or openai")
	}
	switch c.ToolApproval {
	case "", ApproveChanges, ApproveAll:
	default:
		return c, fmt.Errorf("toolApproval must be changes or all")
	}
	switch c.Effort {
	case "", "low", "medium", "high":
	default:
		return c, fmt.Errorf("effort must be low, medium, or high")
	}
	if len(c.Instructions) > MaxInstructions {
		return c, fmt.Errorf("instructions exceed %d bytes", MaxInstructions)
	}
	return c, nil
}

// ModelOrDefault is the model the chat uses.
func (c Configuration) ModelOrDefault() string {
	if c.Model == "" {
		return DefaultModel
	}
	return c.Model
}

// Manager caches the stored configuration and resolves the API key, which
// may also come from the environment (GOFI_ASSISTANT_API_KEY, or a file
// named by GOFI_ASSISTANT_API_KEY_FILE) so a deployment can keep it out of
// the store entirely.
type Manager struct {
	store    Store
	mu       sync.RWMutex
	cached   *Configuration
	loadedAt time.Time
}

// cacheTTL bounds how long a replica serves a configuration another replica
// (or kubectl) changed: a revoked key or allowChanges takes effect within it.
const cacheTTL = 30 * time.Second

func NewManager(store Store) *Manager { return &Manager{store: store} }

func (m *Manager) Backend() string { return m.store.Backend() }

// Get returns the stored configuration, loading it once.
func (m *Manager) Get(ctx context.Context) (Configuration, error) {
	m.mu.RLock()
	if m.cached != nil && time.Since(m.loadedAt) < cacheTTL {
		c := *m.cached
		m.mu.RUnlock()
		return c, nil
	}
	m.mu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cached != nil && time.Since(m.loadedAt) < cacheTTL {
		return *m.cached, nil
	}
	c, err := m.store.Load(ctx)
	if err != nil {
		if m.cached != nil {
			return *m.cached, nil // a transient store error keeps the last good value
		}
		return Configuration{}, err
	}
	c, err = Normalize(c)
	if err != nil {
		return Configuration{}, fmt.Errorf("stored assistant configuration: %w", err)
	}
	m.cached, m.loadedAt = &c, time.Now()
	return c, nil
}

// Save validates and stores c. An empty APIKey keeps the stored key, so a
// portal form that never sees the key can round-trip it; "***" (the
// redaction sentinel) is treated the same way. clearKey forgets it instead.
func (m *Manager) Save(ctx context.Context, c Configuration, clearKey ...bool) (Configuration, error) {
	c, err := Normalize(c)
	if err != nil {
		return c, err
	}
	locks := EnvironmentLocks()
	if locks.Endpoint && c.BaseURL != "" && c.BaseURL != strings.TrimSpace(os.Getenv("GOFI_ASSISTANT_BASE_URL")) {
		return c, fmt.Errorf("the endpoint is pinned by GOFI_ASSISTANT_BASE_URL and cannot be changed here")
	}
	if locks.Key && c.APIKey != "" && c.APIKey != "***" {
		return c, fmt.Errorf("the API key is set by the environment and cannot be changed here")
	}
	if pinned, ok := pinnedAllowFunctions(); ok && c.AllowFunctions != pinned {
		return c, fmt.Errorf("whether functions may use the AI endpoint is pinned by GOFI_ASSISTANT_ALLOW_FUNCTIONS and cannot be changed here")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(clearKey) > 0 && clearKey[0] {
		c.APIKey = ""
	} else if c.APIKey == "" || c.APIKey == "***" {
		if m.cached == nil {
			if stored, err := m.store.Load(ctx); err == nil {
				m.cached = &stored
			}
		}
		if m.cached != nil {
			c.APIKey = m.cached.APIKey
		}
	}
	if err := m.store.Save(ctx, c); err != nil {
		return c, err
	}
	m.cached, m.loadedAt = &c, time.Now()
	return c, nil
}

// Key resolves the API key for c: the environment first (an operator-pinned
// key that portal users cannot replace), else the stored key. source is
// "stored", "environment", or "none".
func Key(c Configuration) (key, source string) {
	if v := environmentKey(); v != "" {
		return v, "environment"
	}
	if c.APIKey != "" && c.APIKey != "***" {
		return c.APIKey, "stored"
	}
	return "", "none"
}

func environmentKey() string {
	if v := strings.TrimSpace(os.Getenv("GOFI_ASSISTANT_API_KEY")); v != "" {
		return v
	}
	if path := os.Getenv("GOFI_ASSISTANT_API_KEY_FILE"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

// Locks reports what the environment pins. There is no in-app authorization,
// so every portal user can edit the configuration; an operator who sets
// GOFI_ASSISTANT_BASE_URL or GOFI_ASSISTANT_API_KEY(_FILE) keeps the endpoint
// and the key out of their hands: saves that would change them are refused
// and the controller only ever talks to the pinned endpoint.
type Locks struct {
	Endpoint bool
	Key      bool
	// Functions: GOFI_ASSISTANT_ALLOW_FUNCTIONS pins whether Python
	// libraries may use the endpoint, so an operator can hold that off (or
	// on) regardless of what portal users save.
	Functions bool
}

func EnvironmentLocks() Locks {
	_, functions := pinnedAllowFunctions()
	return Locks{Endpoint: strings.TrimSpace(os.Getenv("GOFI_ASSISTANT_BASE_URL")) != "", Key: environmentKey() != "", Functions: functions}
}

// pinnedAllowFunctions reads GOFI_ASSISTANT_ALLOW_FUNCTIONS: "1", "true",
// "yes", "on" pin the switch on; "0", "false", "no", "off" pin it off;
// unset or anything else leaves it to the configuration.
func pinnedAllowFunctions() (value, ok bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GOFI_ASSISTANT_ALLOW_FUNCTIONS"))) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// Effective is c with the environment's pins applied: the base URL the
// controller will actually call, and the functions switch when pinned.
func Effective(c Configuration) Configuration {
	if v := strings.TrimSpace(os.Getenv("GOFI_ASSISTANT_BASE_URL")); v != "" {
		c.BaseURL = v
	}
	if pinned, ok := pinnedAllowFunctions(); ok {
		c.AllowFunctions = pinned
	}
	return c
}

// Public is c as a read shows it: the key withheld.
func Public(c Configuration) Configuration {
	c.APIKey = ""
	return c
}
