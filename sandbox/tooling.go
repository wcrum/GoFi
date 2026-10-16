package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
)

const toolingSource = "import gofi\n@gofi.parser(\"noop\")\ndef noop(raw):\n    return {}\n"

// ToolingBinding starts a private fixed-source worker lazily, before any user
// library is saved. Editor source is supplied only to its static tooling RPCs,
// never used as a worker startup definition or imported for formatting/linting.
func (s *Supervisor) ToolingBinding(ctx context.Context) (Binding, error) {
	s.toolingMu.Lock()
	defer s.toolingMu.Unlock()
	if err := ctx.Err(); err != nil {
		return Binding{}, err
	}
	if s.backend == nil || s.toolingClosed {
		return Binding{}, fmt.Errorf("Python tooling backend is unavailable")
	}
	if s.toolingContext != nil {
		callCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(s.toolingContext, cancel)
		defer stop()
		defer cancel()
		ctx = callCtx
	}
	if s.tooling.Endpoint != "" {
		if health, ok := s.backend.(HealthBackend); !ok || health.Health(ctx, s.tooling) == nil {
			return s.tooling, nil
		}
	}
	if s.toolingName == "" {
		var identity [8]byte
		if _, err := rand.Read(identity[:]); err != nil {
			return Binding{}, err
		}
		s.toolingName = "gofi-tools-" + hex.EncodeToString(identity[:])
	}
	var credential [32]byte
	if _, err := rand.Read(credential[:]); err != nil {
		return Binding{}, err
	}
	token := hex.EncodeToString(credential[:])
	definition := Definition{Name: s.toolingName, SourceCode: toolingSource, TimeoutSeconds: 5, MemoryMiB: 128}
	endpoint, err := s.backend.Start(ctx, definition, token)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.backend.Stop(cleanup, s.toolingName)
		s.tooling = Binding{}
		return Binding{}, err
	}
	s.tooling = Binding{Endpoint: endpoint, Token: token, TimeoutSeconds: 5}
	return s.tooling, nil
}

// CloseTooling removes only this supervisor's private tooling worker. Public
// durable libraries retain their own normal runtime lifecycle.
func (s *Supervisor) CloseTooling(ctx context.Context) error {
	if s.toolingCancel != nil {
		s.toolingCancel()
	}
	s.toolingMu.Lock()
	defer s.toolingMu.Unlock()
	s.toolingClosed = true
	s.tooling = Binding{}
	if s.backend == nil || s.toolingName == "" {
		return nil
	}
	if err := s.backend.Stop(ctx, s.toolingName); err != nil {
		return err
	}
	s.toolingName = ""
	return nil
}

// ValidateStatic checks source in the private tooling worker. The source is
// parsed and linted there, never imported or executed.
func (s *Supervisor) ValidateStatic(ctx context.Context, source string) (*gofiv1.WorkerValidateSourceResponse, error) {
	binding, err := s.ToolingBinding(ctx)
	if err != nil {
		return nil, fmt.Errorf("Python validation worker unavailable: %w", err)
	}
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	call := connect.NewRequest(&gofiv1.WorkerValidateSourceRequest{Source: source})
	call.Header().Set("Authorization", "Bearer "+binding.Token)
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, err := gofiv1connect.NewWorkerServiceClient(&http.Client{Transport: transport}, binding.Endpoint, connect.WithReadMaxBytes(2<<20)).ValidateSource(callCtx, call)
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

// DescribeRuntime asks the tooling worker for the Python runtime it shares
// with every library worker (same image), without running user source.
func (s *Supervisor) DescribeRuntime(ctx context.Context) (*gofiv1.DescribePythonEnvironmentResponse, error) {
	binding, err := s.ToolingBinding(ctx)
	if err != nil {
		return nil, fmt.Errorf("Python tooling worker unavailable: %w", err)
	}
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	call := connect.NewRequest(&gofiv1.DescribePythonEnvironmentRequest{})
	call.Header().Set("Authorization", "Bearer "+binding.Token)
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, err := gofiv1connect.NewWorkerServiceClient(&http.Client{Transport: transport}, binding.Endpoint, connect.WithReadMaxBytes(2<<20)).DescribeEnvironment(callCtx, call)
	if err != nil {
		return nil, err
	}
	out := response.Msg
	out.NetworkPolicy = s.EgressPolicy()
	out.MaxTimeoutSeconds, out.MaxMemoryMib = 30, 1024
	return out, nil
}

// EgressPolicy describes how this deployment enforces library egress.
func (s *Supervisor) EgressPolicy() string {
	switch s.backend.(type) {
	case *Kubernetes:
		return KubernetesEgressPolicy
	case *Docker:
		return DockerEgressPolicy
	default:
		return "Workers have no outbound network access unless a library declares egress rules."
	}
}
