package sink

import (
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// newTransport returns an http.Transport tuned for a sink that sends many
// small requests to one host: a large idle pool so concurrent connections do
// not re-dial (the default keeps two idle connections per host), and
// bounded dial, TLS, header, and idle timeouts so a stalled destination
// cannot hold the worker. Each sink owns one transport, shared across all
// of its messages.
func newTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = httpMaxIdleConns() * 2
	t.MaxIdleConnsPerHost = httpMaxIdleConns()
	t.IdleConnTimeout = 90 * time.Second
	t.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = 5 * time.Second
	t.ResponseHeaderTimeout = 20 * time.Second
	t.ExpectContinueTimeout = time.Second
	return t
}

// transientError marks a failure worth retrying.
type transientError struct{ error }

func (e transientError) Retryable() bool { return true }
func (e transientError) Unwrap() error   { return e.error }

// Retryable reports whether a send failure is transient: a network error,
// an HTTP 429 or 5xx, or an error a sink marked retryable. 4xx responses
// and encoding failures are not.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	var marked interface{ Retryable() bool }
	if errors.As(err, &marked) {
		return marked.Retryable()
	}
	var status *StatusError
	if errors.As(err, &status) {
		return status.Code == http.StatusTooManyRequests || status.Code >= 500
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE)
}
