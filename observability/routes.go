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
	"sync/atomic"
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

// routeHealth is the live record behind a RouteHealth. The success path is
// lock-free (atomic counters and timestamps); only a failure, which also
// stores the error text, takes the record's lock.
type routeHealth struct {
	route       string
	successes   atomic.Uint64
	failures    atomic.Uint64
	lastSuccess atomic.Int64 // unix nanoseconds; 0 = never
	lastFailure atomic.Int64

	mu         sync.Mutex
	lastError  string
	errorClass string
}

func (h *routeHealth) record(flow string, err error) {
	now := time.Now()
	if err == nil {
		h.successes.Add(1)
		h.lastSuccess.Store(now.UnixNano())
		return
	}
	h.failures.Add(1)
	h.lastFailure.Store(now.UnixNano())
	h.mu.Lock()
	h.lastError = truncateText(err.Error(), maxRouteError)
	h.errorClass = ClassifyError(err)
	h.mu.Unlock()
	// The board has its own lock; Report never takes routes'.
	ReportError(flow, RouteComponent(h.route), err)
}

func (h *routeHealth) snapshot() RouteHealth {
	out := RouteHealth{Route: h.route, Successes: h.successes.Load(), Failures: h.failures.Load()}
	if ns := h.lastSuccess.Load(); ns != 0 {
		out.LastSuccess = time.Unix(0, ns)
	}
	if ns := h.lastFailure.Load(); ns != 0 {
		out.LastFailure = time.Unix(0, ns)
	}
	h.mu.Lock()
	out.LastError, out.ErrorClass = h.lastError, h.errorClass
	h.mu.Unlock()
	return out
}

var routes = struct {
	sync.RWMutex
	byFlow map[string]map[string]*routeHealth
	// generation changes on every ResetRoutes so cached Route handles notice
	// that their record was replaced.
	generation atomic.Uint64
}{byFlow: map[string]map[string]*routeHealth{}}

// maxRouteError bounds the stored error text.
const maxRouteError = 300

func lookupRoute(flow, route string) *routeHealth {
	routes.RLock()
	health := routes.byFlow[flow][route]
	routes.RUnlock()
	if health != nil {
		return health
	}
	routes.Lock()
	defer routes.Unlock()
	flowRoutes := routes.byFlow[flow]
	if flowRoutes == nil {
		flowRoutes = map[string]*routeHealth{}
		routes.byFlow[flow] = flowRoutes
	}
	health = flowRoutes[route]
	if health == nil {
		health = &routeHealth{route: route}
		flowRoutes[route] = health
	}
	return health
}

// RouteResult records one connection or send attempt on a route; err nil is
// a success. Hot paths should hold a Route instead and call Result on it.
func RouteResult(flow, route string, err error) {
	lookupRoute(flow, route).record(flow, err)
}

// Route is a cached handle on one route's health record. Result costs two
// atomic stores on success and never takes the global routes lock unless
// ResetRoutes replaced the record.
type Route struct {
	flow, route string
	generation  uint64
	health      *routeHealth
	mu          sync.Mutex
}

// NewRoute returns the handle for (flow, route).
func NewRoute(flow, route string) *Route {
	return &Route{flow: flow, route: route}
}

func (r *Route) current() *routeHealth {
	gen := routes.generation.Load()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.health == nil || r.generation != gen {
		r.health = lookupRoute(r.flow, r.route)
		r.generation = gen
	}
	return r.health
}

// Result records one attempt on the route; err nil is a success.
func (r *Route) Result(err error) { r.current().record(r.flow, err) }

// truncateText keeps valid UTF-8 of at most n bytes (plus an ellipsis), since
// the text travels in protobuf strings, which must be valid UTF-8.
func truncateText(text string, n int) string {
	text = strings.ToValidUTF8(text, "�")
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
	routes.RLock()
	defer routes.RUnlock()
	out := make([]RouteHealth, 0, len(routes.byFlow[flow]))
	for _, health := range routes.byFlow[flow] {
		out = append(out, health.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route < out[j].Route })
	return out
}

// ResetRoutes forgets a flow's routes, for a listener started with a new
// definition whose routes may differ.
func ResetRoutes(flow string) {
	routes.Lock()
	delete(routes.byFlow, flow)
	routes.generation.Add(1)
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
