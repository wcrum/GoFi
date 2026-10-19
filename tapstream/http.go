package tapstream

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wcrum/gofi/observability"
)

// WriteTimeoutEnv is how long one write to a streaming client may block
// before the stream is closed; a browser that stops reading must not pin a
// goroutine and its queue forever. Default 10s.
const WriteTimeoutEnv = "GOFI_WATCH_WRITE_TIMEOUT"

// WriteTimeout reads the configured per-write deadline.
func WriteTimeout() time.Duration {
	if raw := os.Getenv(WriteTimeoutEnv); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return 10 * time.Second
}

// IsStreamPath reports whether a ConnectRPC procedure path is one of the
// server-streaming methods that get a write deadline.
func IsStreamPath(path string) bool {
	for _, suffix := range []string{"/WatchTraffic", "/WatchFlowStats", "/WatchWorkerLogs", "/AssistantService/Chat"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// WithWriteDeadline arms a write deadline before every write on streaming
// responses, so a stalled client fails the handler's next Send instead of
// blocking it. Other paths pass through untouched.
func WithWriteDeadline(next http.Handler, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsStreamPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&deadlineWriter{ResponseWriter: w, rc: http.NewResponseController(w), timeout: timeout}, r)
	})
}

type deadlineWriter struct {
	http.ResponseWriter
	rc      *http.ResponseController
	timeout time.Duration
	failed  bool
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	_ = d.rc.SetWriteDeadline(time.Now().Add(d.timeout)) // unsupported transports keep streaming without one
	n, err := d.ResponseWriter.Write(p)
	if err != nil && !d.failed && isTimeout(err) {
		d.failed = true
		observability.WatchWriteTimeout()
	}
	return n, err
}

// Flush is what streaming handlers call after every message; the deadline
// armed by Write covers the flush of the same message.
func (d *deadlineWriter) Flush() {
	if err := d.rc.Flush(); err != nil && !d.failed && isTimeout(err) {
		d.failed = true
		observability.WatchWriteTimeout()
	}
}

// Unwrap lets http.ResponseController reach the real writer.
func (d *deadlineWriter) Unwrap() http.ResponseWriter { return d.ResponseWriter }

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.Is(err, os.ErrDeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout()
}
