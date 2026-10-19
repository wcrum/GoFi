package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wcrum/gofi/assistant"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/deploymode"
	"github.com/wcrum/gofi/functionrpc"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/sandbox"
	"github.com/wcrum/gofi/spec"
)

// startSandboxes starts the Python library supervisor and, with it, the AI
// proxy listener (llm_runtime.go) that AI libraries call; the returned
// server is nil when no proxy could be started.
func startSandboxes(ctx context.Context, ctrl *controller.Controller, mode deploymode.Mode, bot *assistant.Manager) (*sandbox.Supervisor, *http.Server) {
	var backend sandbox.Backend
	var store sandbox.Store
	var err error
	if ctrl.DriverKind() == "kubernetes" {
		backend, err = sandbox.NewKubernetes(k8sNamespace(), os.Getenv("GOFI_SANDBOX_IMAGE"))
		if err == nil {
			var kubeStore *sandbox.KubernetesStore
			if kubeStore, err = sandbox.NewKubernetesStore(k8sNamespace()); err == nil {
				kubeStore.Lister = kubeRuntime.lister(sandbox.SandboxGVR)
				store = kubeStore
			}
		}
	} else {
		backend, err = sandbox.NewDocker(sandbox.DockerConfig{Image: os.Getenv("GOFI_SANDBOX_IMAGE"), Network: os.Getenv("GOFI_SANDBOX_NETWORK"), InContainer: mode == deploymode.ModeCompose || mode == deploymode.ModeDocker})
		path := os.Getenv("GOFI_SANDBOX_CONFIG_FILE")
		if path == "" {
			path = filepath.Join(filepath.Dir(ctrl.Configuration().Path), "sandboxes.yaml")
		}
		store = &sandbox.FileStore{Path: path}
	}
	if err != nil {
		slog.Error("Python sandbox initialization failed", "error", err)
		return nil, nil
	}
	manager := sandbox.New(backend, store)
	// The proxy listens before the first reconcile, so AI workers start
	// once with their environment instead of restarting when it appears.
	proxyUp := new(atomic.Bool)
	manager.AIEnvironment = aiEnvironment(bot, backend, listenPort(envOrDefault("GOFI_LLM_PROXY_ADDR", ":9091"), 9091), proxyUp)
	llmServer := startLLMProxy(ctx, manager, bot, backend, proxyUp)
	functionrpc.SetResolver(func(ctx context.Context) (map[string]functionrpc.Binding, error) {
		bindings, err := manager.Bindings(ctx)
		out := map[string]functionrpc.Binding{}
		for name, b := range bindings {
			out[name] = functionrpc.Binding{Endpoint: b.Endpoint, Token: b.Token, TimeoutSeconds: b.TimeoutSeconds}
		}
		return out, err
	})
	functionrpc.SetInlineRunner(func(ctx context.Context, ref *spec.PythonRef, req *gofiv1.WorkerInvokeRequest) (*gofiv1.WorkerInvokeResponse, error) {
		return runInline(ctx, manager, ref, req)
	})
	wake := make(chan struct{}, 1)
	controller.SetInlineHooks(controller.InlineHooks{
		Validate: func(ctx context.Context, def spec.FlowDefinition) error { return validateInline(ctx, manager, def) },
		Changed: func() {
			select {
			case wake <- struct{}{}:
			default:
			}
		},
	})
	go syncManagedLibraries(ctx, ctrl, manager, wake)
	go func() {
		if err := manager.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("Python sandbox reconciliation stopped", "error", err)
		}
	}()
	go func() {
		previous := ""
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				bindings, _ := manager.Bindings(ctx)
				data, _ := json.Marshal(bindings)
				current := string(data)
				if current != previous {
					ctrl.NotifyRuntimeChange()
					previous = current
				}
			}
		}
	}()
	return manager, llmServer
}

// aiEnvironment supplies the deployment's AI endpoint to libraries that opt
// in with `ai`: when the assistant (Administration) is enabled and has a
// key and the AI proxy listener is up, the proxy's address, the provider,
// and the model, plus the egress rules a worker needs to reach the proxy
// port. The model key never leaves the controller; the worker calls the
// proxy with its own token. The address is cached for a minute (the
// supervisor asks on every reconcile).
func aiEnvironment(bot *assistant.Manager, backend sandbox.Backend, proxyPort int, proxyUp *atomic.Bool) sandbox.AIEnvironment {
	reach, ok := backend.(sandbox.ControllerReach)
	if bot == nil || !ok {
		return nil
	}
	var mu sync.Mutex
	var cachedAddr string
	var cachedRules []sandbox.EgressRule
	var cachedErr error
	var cachedAt time.Time
	resolve := func() (string, []sandbox.EgressRule, error) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(cachedAt) < time.Minute && (cachedAddr != "" || cachedErr != nil) {
			return cachedAddr, cachedRules, cachedErr
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		addr, rules, err := reach.ControllerAddress(ctx, proxyPort)
		if err != nil && (cachedErr == nil || cachedErr.Error() != err.Error()) {
			slog.Warn("AI libraries cannot reach the controller's AI proxy", "error", err)
		}
		cachedAddr, cachedRules, cachedErr, cachedAt = addr, rules, err, time.Now()
		return addr, rules, err
	}
	return func() (map[string]string, []sandbox.EgressRule, bool) {
		if !proxyUp.Load() {
			return nil, nil, false
		}
		c, err := bot.Get(context.Background())
		if err != nil {
			return nil, nil, false
		}
		c = assistant.Effective(c)
		if !c.Enabled || !c.AllowFunctions {
			return nil, nil, false
		}
		if key, _ := assistant.Key(c); key == "" {
			return nil, nil, false
		}
		addr, rules, err := resolve()
		if err != nil {
			return nil, nil, false
		}
		env := map[string]string{
			sandbox.AIEnvProxyURL: "http://" + addr,
			sandbox.AIEnvProvider: c.ProviderOrDefault(),
			sandbox.AIEnvModel:    c.ModelOrDefault(),
		}
		return env, rules, true
	}
}

func toFunctionBinding(b sandbox.Binding) functionrpc.Binding {
	return functionrpc.Binding{Endpoint: b.Endpoint, Token: b.Token, TimeoutSeconds: b.TimeoutSeconds}
}

// runInline executes an unresolved inline stage (an editor simulation). A
// ready managed worker already running the identical snippet is reused;
// otherwise a scratch worker runs it, so unsaved edits are what executes.
func runInline(ctx context.Context, manager *sandbox.Supervisor, ref *spec.PythonRef, req *gofiv1.WorkerInvokeRequest) (*gofiv1.WorkerInvokeResponse, error) {
	if binding, ok := manager.InlineBinding(ref.Function, ref.InlineSource); ok {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(max(binding.TimeoutSeconds, 1))*time.Second)
		defer cancel()
		return functionrpc.Call(callCtx, toFunctionBinding(binding), req)
	}
	source := "import gofi as _gofi\n" + sandbox.WrapInline(ref.Function, ref.InlineSource)
	var out *gofiv1.WorkerInvokeResponse
	err := manager.RunScratch(ctx, sandbox.Definition{SourceCode: source}, func(ctx context.Context, binding sandbox.Binding) error {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(binding.TimeoutSeconds+5)*time.Second)
		defer cancel()
		var err error
		out, err = functionrpc.Call(callCtx, toFunctionBinding(binding), req)
		return err
	})
	return out, err
}

// validateInline statically checks every inline stage (parsed and linted in
// the tooling worker, never executed). Diagnostics use snippet line numbers.
func validateInline(ctx context.Context, manager *sandbox.Supervisor, def spec.FlowDefinition) error {
	if err := validatePythonSource(ctx, manager, def.Source.Python); err != nil {
		return err
	}
	total := 0
	for _, stage := range def.InlineStages() {
		total += len(stage.Python.InlineSource)
		result, err := manager.ValidateStatic(ctx, stage.Python.InlineSource)
		if err != nil {
			return fmt.Errorf("stage %q: inline Python cannot be validated: %w", stage.ID, err)
		}
		if !result.Valid {
			problems := make([]string, 0, len(result.Diagnostics))
			for _, d := range result.Diagnostics {
				problems = append(problems, fmt.Sprintf("line %d:%d %s %s", d.Line, d.Column, d.Code, d.Message))
			}
			return fmt.Errorf("stage %q: inline Python is invalid: %s", stage.ID, strings.Join(problems, "; "))
		}
	}
	if total > 60000 {
		return fmt.Errorf("inline Python stages exceed the 64 KiB library limit together")
	}
	return nil
}

// syncManagedLibraries keeps one managed library per listener with inline
// stages and removes managed libraries whose listener no longer needs one.
// User libraries are never touched.
func syncManagedLibraries(ctx context.Context, ctrl *controller.Controller, manager *sandbox.Supervisor, wake <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if manager.Passive() {
			// A standby replica leaves managed libraries to the leader.
			select {
			case <-ctx.Done():
				return
			case <-wake:
			case <-ticker.C:
			}
			continue
		}
		want := map[string]sandbox.Definition{}
		for _, def := range ctrl.List() {
			if lib, ok := sandbox.InlineLibrary(def); ok {
				want[lib.Name] = lib
			}
		}
		for name, lib := range want {
			current, exists := manager.Get(name)
			status, _ := manager.Status(name)
			if exists && current.SourceCode == lib.SourceCode && current.ManagedBy == lib.ManagedBy && status.Phase == "Ready" {
				continue
			}
			if exists && !current.Managed() {
				slog.Error("managed library name collides with a user library", "library", name)
				continue
			}
			applyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			if err := manager.Apply(applyCtx, lib); err != nil {
				slog.Error("managed Python library start failed", "library", name, "listener", strings.TrimPrefix(lib.ManagedBy, "flow:"), "error", err)
			} else {
				slog.Info("managed Python library ready", "library", name, "listener", strings.TrimPrefix(lib.ManagedBy, "flow:"))
			}
			cancel()
		}
		for _, d := range manager.List() {
			if _, needed := want[d.Name]; d.Managed() && !needed {
				deleteCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
				if err := manager.Delete(deleteCtx, d.Name); err != nil {
					slog.Warn("managed Python library removal failed", "library", d.Name, "error", err)
				} else {
					slog.Info("managed Python library removed", "library", d.Name)
				}
				cancel()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
	}
}

// validatePythonSource checks a source.python reference against the
// library's registry: the library must exist, and when its worker is ready
// the function must be a @gofi.fetcher or @gofi.streamer. A library that is
// still starting is accepted; the flow runtime retries until it is ready.
func validatePythonSource(ctx context.Context, manager *sandbox.Supervisor, src *spec.PythonSource) error {
	if src == nil {
		return nil
	}
	lib, ok := manager.Get(src.Library)
	if !ok {
		return fmt.Errorf("source.python: Python library %q does not exist", src.Library)
	}
	if lib.Managed() {
		return fmt.Errorf("source.python: library %q is generated from a flow's inline stages; use a saved library", src.Library)
	}
	bindings, _ := manager.Bindings(ctx)
	binding, ready := bindings[src.Library]
	if !ready {
		return nil
	}
	functions, err := functionrpc.ListFunctions(ctx, toFunctionBinding(binding))
	if err != nil {
		slog.Warn("Python source function not verified: library worker unreachable", "library", src.Library, "error", err)
		return nil
	}
	return checkSourceFunction(src, functions)
}

func checkSourceFunction(src *spec.PythonSource, functions []*gofiv1.WorkerFunction) error {
	var kinds []string
	for _, f := range functions {
		if f.Name != src.Function {
			continue
		}
		if spec.CheckSourceKind(src.Library, src.Function, f.Kind) == nil {
			return nil
		}
		kinds = append(kinds, f.Kind)
	}
	if len(kinds) > 0 {
		return spec.CheckSourceKind(src.Library, src.Function, kinds[0])
	}
	return fmt.Errorf("source.python: library %q has no @gofi.fetcher or @gofi.streamer named %q", src.Library, src.Function)
}
