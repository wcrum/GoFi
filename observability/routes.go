package observability

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// Route IDs name the outbound connections a flow makes. They match the path
// IDs of the Kubernetes connectivity analysis, so an observed failure joins
// the Package verdict for the same path.
const (
	RouteSource     = "egress-source"
	RouteDeadLetter = "egress-deadletter"
)

// RouteSink is the route ID of a flow's i-th destination (0-based).
func RouteSink(i int) string { return "egress-sink-" + strconv.Itoa(i) }

// RouteHealth is what this process has seen on one route since the flow
// started: counts, the last outcome each way, and the last error.
type RouteHealth struct {
	Route       string
	Successes   uint64
	Failures    uint64
	LastSuccess time.Time
	LastFailure time.Time
	LastError   string
	// ErrorClass is ClassifyError of the last failure.
	ErrorClass string
}

// Failing reports whether the most recent attempt failed.
func (h RouteHealth) Failing() bool { return h.LastFailure.After(h.LastSuccess) }

var routes = struct {
	sync.Mutex
	byFlow map[string]map[string]*RouteHealth
}{byFlow: map[string]map[string]*RouteHealth{}}

// maxRouteError bounds the stored error text.
const maxRouteError = 300

// RouteResult records one connection or send attempt on a route; err nil is
// a success.
func RouteResult(flow, route string, err error) {
	now := time.Now()
	routes.Lock()
	defer routes.Unlock()
	flowRoutes := routes.byFlow[flow]
	if flowRoutes == nil {
		flowRoutes = map[string]*RouteHealth{}
		routes.byFlow[flow] = flowRoutes
	}
	health := flowRoutes[route]
	if health == nil {
		health = &RouteHealth{Route: route}
		flowRoutes[route] = health
	}
	if err == nil {
		health.Successes++
		health.LastSuccess = now
		return
	}
	health.Failures++
	health.LastFailure = now
	health.LastError = truncateText(err.Error(), maxRouteError)
	health.ErrorClass = ClassifyError(err)
}

// truncateText keeps valid UTF-8 of at most n bytes (plus an ellipsis), since
// the text travels in protobuf strings, which must be valid UTF-8.
func truncateText(text string, n int) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	if len(text) <= n {
		return text
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// Routes returns a flow's routes, sorted by ID.
func Routes(flow string) []RouteHealth {
	routes.Lock()
	defer routes.Unlock()
	out := make([]RouteHealth, 0, len(routes.byFlow[flow]))
	for _, health := range routes.byFlow[flow] {
		out = append(out, *health)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route < out[j].Route })
	return out
}

// ResetRoutes forgets a flow's routes, for a listener started with a new
// definition whose routes may differ.
func ResetRoutes(flow string) {
	routes.Lock()
	delete(routes.byFlow, flow)
	routes.Unlock()
}

// ClassifyError names why a connection or send failed, as far as the socket
// shows it. A dropped packet (NetworkPolicy) usually surfaces as "timeout";
// Istio ambient denies TCP by accepting through ztunnel and then closing,
// which surfaces as "reset"; a waypoint denies HTTP with 403 "RBAC: access
// denied" ("denied"). None of these proves a policy decision on its own.
//
// Classes: denied, forbidden, http, dns, refused, reset, timeout,
// unreachable, tls, other.
func ClassifyError(err error) string {
	var denied interface{ PolicyDenied() bool }
	if errors.As(err, &denied) && denied.PolicyDenied() {
		return "denied"
	}
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) {
		if status.HTTPStatus() == 403 {
			return "forbidden"
		}
		return "http"
	}
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var timeout interface{ Timeout() bool }
	switch {
	case errors.As(err, &dns):
		return "dns"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "reset"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "unreachable"
	case errors.As(err, &certificate), errors.As(err, &record), errors.As(err, &unknownAuthority), errors.As(err, &hostname):
		return "tls"
	case errors.Is(err, os.ErrDeadlineExceeded), errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return "timeout"
	}
	return "other"
}
