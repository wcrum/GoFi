package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/wcrum/gofi/api"
	"github.com/wcrum/gofi/assistant"
	"github.com/wcrum/gofi/sandbox"
)

// startLLMProxy serves the AI proxy (api.NewLLMProxy) on its own listener,
// apart from the API server, so an AI library's egress reaches the proxy
// and nothing else (the portal's RPCs are unauthenticated). The port comes
// from GOFI_LLM_PROXY_ADDR (default :9091). Where workers can reach the
// controller only through one address (Docker's sandbox network), the
// listener binds that address alone: the controller's own interface on the
// network when it is a container, else the network's gateway, never
// 0.0.0.0 on a host. When that address is unknown or the bind fails, the
// proxy is not started and AI stays unavailable (up stays false; the
// supervisor's hook then supplies nothing), with the reason logged.
func startLLMProxy(ctx context.Context, manager *sandbox.Supervisor, bot *assistant.Manager, backend sandbox.Backend, up *atomic.Bool) *http.Server {
	if manager == nil || bot == nil {
		return nil
	}
	addr := envOrDefault("GOFI_LLM_PROXY_ADDR", ":9091")
	port := listenPort(addr, 9091)
	if binder, ok := backend.(sandbox.ProxyBinder); ok {
		bindCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		bound, err := binder.ProxyBindAddress(bindCtx, port)
		if err != nil {
			slog.Warn("AI proxy not started: the controller's address on the sandbox network is unknown, so AI libraries stay unavailable", "error", err)
			return nil
		}
		addr = bound
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("AI proxy bind failed; AI libraries stay unavailable", "addr", addr, "error", err)
		return nil
	}
	srv := &http.Server{Handler: api.NewLLMProxy(manager, bot, llmMaxOutputTokens()), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	up.Store(true)
	go func() {
		slog.Info("AI proxy listening", "addr", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			up.Store(false)
			slog.Error("AI proxy server error; AI libraries stay unavailable", "error", err)
		}
	}()
	return srv
}

// llmMaxOutputTokens is GOFI_LLM_MAX_OUTPUT_TOKENS, the cap the proxy puts
// on max_tokens / max_completion_tokens (default api.LLMDefaultMaxOutputTokens).
func llmMaxOutputTokens() int {
	if raw := os.Getenv("GOFI_LLM_MAX_OUTPUT_TOKENS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
		slog.Warn("GOFI_LLM_MAX_OUTPUT_TOKENS is not a positive integer; using the default", "value", raw, "default", api.LLMDefaultMaxOutputTokens)
	}
	return api.LLMDefaultMaxOutputTokens
}

// listenPort is the port of a listen address (":9091", "10.0.0.1:9091").
func listenPort(addr string, fallback int) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fallback
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fallback
	}
	return n
}
