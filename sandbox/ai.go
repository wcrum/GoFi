package sandbox

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"maps"
	"slices"
)

// AIEnvironment supplies the deployment's AI endpoint (the assistant
// configuration under Administration, while its "functions may use the AI
// endpoint" switch is on) to every library. The model key never reaches a worker: the worker gets the
// controller's LLM proxy address, the provider, and the model, and calls the
// proxy with its own sandbox token (gofi.llm). The egress rules are what a
// worker needs to reach the controller; empty when its network already
// carries the controller (Docker's internal sandbox network).
//
// ok is false when the deployment has no enabled endpoint with a key or
// the switch is off; workers then start without the variables and gofi.llm
// reports that AI is unavailable. The variables decide restarts (a changed
// provider, model, or proxy address, or flipping the switch, restarts every
// worker); the egress rules are read when a worker starts.
type AIEnvironment func() (env map[string]string, egress []EgressRule, ok bool)

// The variables an AIEnvironment supplies to a worker.
const (
	AIEnvProxyURL  = "GOFI_LLM_PROXY_URL" // http://<controller>:<proxy port>
	AIEnvProvider  = "GOFI_LLM_PROVIDER"  // "anthropic" | "openai": which client gofi.llm builds
	AIEnvModel     = "GOFI_LLM_MODEL"     // the deployment's model
	AIEgressReason = "GoFi controller (AI proxy)"
)

// ControllerReach is a backend that knows how a worker reaches the
// controller's LLM proxy: the host:port to dial and the egress rules the
// worker needs for it, which admit the proxy port only (none when the
// worker's network already carries the controller and cannot be widened).
type ControllerReach interface {
	ControllerAddress(ctx context.Context, proxyPort int) (hostPort string, egress []EgressRule, err error)
}

// ProxyBinder is a backend whose workers reach the controller through one
// address only: the LLM proxy listener binds there and nowhere else (the
// Docker sandbox network's address, never 0.0.0.0 on a host).
type ProxyBinder interface {
	ProxyBindAddress(ctx context.Context, proxyPort int) (hostPort string, err error)
}

func (s *Supervisor) aiEnvironment() (map[string]string, []EgressRule, bool) {
	if s == nil || s.AIEnvironment == nil {
		return nil, nil, false
	}
	env, rules, ok := s.AIEnvironment()
	if !ok || len(env) == 0 {
		return nil, nil, false
	}
	return env, rules, true
}

// AIAvailable reports whether the deployment currently provides an AI
// endpoint to libraries.
func (s *Supervisor) AIAvailable() bool {
	_, _, ok := s.aiEnvironment()
	return ok
}

// resolveAI attaches the AI variables to a library while the deployment
// offers them (and clears them otherwise). Only the variables are attached
// here, because they decide restarts; the egress rules are added by
// forBackend when the worker starts.
func (s *Supervisor) resolveAI(d Definition) Definition {
	d.Env = nil
	if env, _, ok := s.aiEnvironment(); ok {
		d.Env = maps.Clone(env)
	}
	return d
}

// forBackend is the definition the backend starts: a library carrying the
// AI environment also gets the egress rules that reach the controller.
func (s *Supervisor) forBackend(d Definition) Definition {
	if len(d.Env) == 0 {
		return d
	}
	_, rules, ok := s.aiEnvironment()
	if !ok || len(rules) == 0 {
		return d
	}
	rules, err := normalizeEgress(rules)
	if err != nil {
		slog.Warn("AI proxy egress rules are invalid; the AI library starts without them", "library", d.Name, "error", err)
		return d
	}
	for i := range rules {
		rules[i].Description = AIEgressReason
	}
	d.Egress = append(slices.Clone(d.Egress), rules...)
	return d
}

// LibraryByToken finds the library (saved or scratch) whose worker holds
// token, comparing in constant time. It authenticates a worker's calls to
// the controller's LLM proxy.
func (s *Supervisor) LibraryByToken(token string) (Definition, bool) {
	if token == "" {
		return Definition{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found Definition
	ok := false
	for name, b := range s.bindings {
		if subtle.ConstantTimeCompare([]byte(b.Token), []byte(token)) == 1 {
			if d, exists := s.defs[name]; exists {
				found, ok = d, true
			}
		}
	}
	for name, t := range s.scratch {
		if subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1 {
			if d, exists := s.scratchDefs[name]; exists {
				found, ok = d, true
			}
		}
	}
	return found, ok
}

// sortedEnv lists a definition's environment as sorted "KEY=value" pairs.
func sortedEnv(env map[string]string) []string {
	keys := slices.Sorted(maps.Keys(env))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
