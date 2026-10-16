package api

import (
	"github.com/go-chi/chi/v5/middleware"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// structuredRequests avoids payloads, query parameters, credentials, and peer
// addresses. Response wrappers preserve Flush for long-running Connect streams.
func structuredRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("http handler panic", "method", r.Method, "path", r.URL.Path, "stack", string(debug.Stack()))
				if wrapped.Status() == 0 {
					http.Error(wrapped, "internal server error", http.StatusInternalServerError)
				}
			}
			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}
			slog.Info("http request", "method", r.Method, "path", r.URL.Path, "status", status, "duration_ms", float64(time.Since(start).Microseconds())/1000, "response_bytes", wrapped.BytesWritten())
		}()
		next.ServeHTTP(wrapped, r)
	})
}
