package api

import (
	"errors"
	"net"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/observability"
)

// streamCaps bounds open browser streams per controller and per client, so a
// tab storm or a reconnect loop cannot exhaust the controller.
type streamCaps struct {
	max, maxPerClient int
	mu                sync.Mutex
	total             int
	perClient         map[string]int
}

func newStreamCaps(cfg hubConfig) *streamCaps {
	return &streamCaps{max: cfg.maxStreams, maxPerClient: cfg.maxPerClient, perClient: map[string]int{}}
}

var errTooManyStreams = errors.New("too many open live streams; close other GoFi tabs or raise GOFI_WATCH_MAX_STREAMS")
var errTooManyClientStreams = errors.New("too many open live streams from this client; close other GoFi tabs or raise GOFI_WATCH_MAX_STREAMS_PER_CLIENT")

// acquire reserves one stream for client and counts it in the kind's gauge.
func (c *streamCaps) acquire(kind, client string) (release func(), err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.total >= c.max {
		observability.WatchStreamRejected("global")
		return nil, rpcError(connect.CodeResourceExhausted, errTooManyStreams)
	}
	if c.perClient[client] >= c.maxPerClient {
		observability.WatchStreamRejected("client")
		return nil, rpcError(connect.CodeResourceExhausted, errTooManyClientStreams)
	}
	c.total++
	c.perClient[client]++
	observability.WatchStream(kind, 1)
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			c.total--
			if c.perClient[client]--; c.perClient[client] <= 0 {
				delete(c.perClient, client)
			}
			c.mu.Unlock()
			observability.WatchStream(kind, -1)
		})
	}, nil
}

// open reports how many streams are open in total.
func (c *streamCaps) open() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// clientKey identifies the browser behind a request: the first hop of
// X-Forwarded-For when an ingress sets it, else the peer address.
func clientKey(req connect.AnyRequest) string {
	if forwarded := req.Header().Get("X-Forwarded-For"); forwarded != "" {
		if first, _, ok := strings.Cut(forwarded, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(forwarded)
	}
	addr := req.Peer().Addr
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
