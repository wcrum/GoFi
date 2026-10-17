package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"
)

type statusError struct {
	code   int
	denied bool
}

func (e statusError) Error() string      { return fmt.Sprintf("HTTP %d", e.code) }
func (e statusError) HTTPStatus() int    { return e.code }
func (e statusError) PolicyDenied() bool { return e.denied }

func TestClassifyError(t *testing.T) {
	dial := func(err error) error { return &net.OpError{Op: "dial", Net: "tcp", Err: err} }
	for want, err := range map[string]error{
		"denied":      fmt.Errorf("http sink: %w", statusError{403, true}),
		"forbidden":   statusError{403, false},
		"http":        statusError{502, false},
		"dns":         dial(&net.DNSError{Err: "no such host", Name: "x.invalid"}),
		"refused":     dial(syscall.ECONNREFUSED),
		"reset":       fmt.Errorf("tcp sink: write: %w", dial(syscall.ECONNRESET)),
		"unreachable": dial(syscall.EHOSTUNREACH),
		"timeout":     fmt.Errorf("fetch: %w", context.DeadlineExceeded),
		"other":       errors.New("boom"),
	} {
		if got := ClassifyError(err); got != want {
			t.Errorf("%v: got %s, want %s", err, got, want)
		}
	}
	if got := ClassifyError(io.EOF); got != "reset" {
		t.Errorf("EOF after connect is a close: %s", got)
	}
}

func TestRouteResultTracksLastOutcome(t *testing.T) {
	ResetRoutes("f")
	defer ResetRoutes("f")
	RouteResult("f", RouteSink(1), nil)
	RouteResult("f", RouteSink(1), &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED})
	RouteResult("f", RouteSource, nil)
	routes := Routes("f")
	if len(routes) != 2 || routes[0].Route != "egress-sink-1" || routes[1].Route != RouteSource {
		t.Fatalf("routes: %+v", routes)
	}
	sink := routes[0]
	if !sink.Failing() || sink.Successes != 1 || sink.Failures != 1 || sink.ErrorClass != "refused" {
		t.Fatalf("sink: %+v", sink)
	}
	if routes[1].Failing() {
		t.Fatalf("source: %+v", routes[1])
	}
	RouteResult("f", RouteSink(1), nil)
	if Routes("f")[0].Failing() {
		t.Fatal("a later success clears failing")
	}
	ResetRoutes("f")
	if len(Routes("f")) != 0 {
		t.Fatal("reset forgets routes")
	}
}

func TestRouteErrorsStayValidUTF8(t *testing.T) {
	ResetRoutes("u")
	defer ResetRoutes("u")
	RouteResult("u", RouteSource, errors.New(strings.Repeat("a", maxRouteError-1)+"é tail"))
	RouteResult("u", RouteSink(0), errors.New("bad \xff byte"))
	for _, route := range Routes("u") {
		if !utf8.ValidString(route.LastError) {
			t.Fatalf("%s: invalid UTF-8 %q", route.Route, route.LastError)
		}
	}
}
