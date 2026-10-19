package api

import (
	"context"
	"crypto/tls"
	"math/rand/v2"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/wcrum/gofi/config"
	"github.com/wcrum/gofi/controller"
	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/observability"
)

// Streaming knobs come from the shared config package (GOFI_WATCH_MAX_STREAMS,
// GOFI_WATCH_MAX_STREAMS_PER_CLIENT, GOFI_WATCH_QUEUE, GOFI_HUB_IDLE_GRACE,
// GOFI_HUB_RESYNC; docs/configuration.md). Every browser stream is served from
// one controller-side subscription per flow, so these bound the controller's
// own cost rather than the number of people watching.
type hubConfig struct {
	// queue is the per-browser queue length (events); a browser that falls
	// further behind loses events, reported on its stream's `dropped` field.
	queue int
	// idleGrace is how long a flow's upstream stays open after its last
	// viewer leaves, so a page change within the grace reuses it.
	idleGrace time.Duration
	// resync is how often a watched flow's runtime target is re-resolved
	// while no Pod watcher pushes changes.
	resync time.Duration
	// maxStreams and maxPerClient cap open browser streams (traffic + stats)
	// per controller and per client IP.
	maxStreams   int
	maxPerClient int
}

func loadHubConfig() hubConfig {
	o := config.Get().Observability
	return hubConfig{queue: o.WatchQueue, idleGrace: o.HubIdleGrace, resync: o.HubResync, maxStreams: o.WatchMaxStreams, maxPerClient: o.WatchMaxStreamsPerClient}
}

// upstreamClients holds the controller's shared connections to runtimes:
// cleartext HTTP/2 (every stream to one Pod multiplexes over one TCP
// connection, dead peers are detected by PING) with an HTTP/1.1 fallback for
// a runtime image that predates h2c.
type upstreamClients struct {
	h2, h1 *http.Client
	mu     sync.Mutex
	h1Only map[string]bool
}

func newUpstreamClients() *upstreamClients {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	h2 := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		ReadIdleTimeout: 30 * time.Second, PingTimeout: 10 * time.Second,
	}
	h1 := &http.Transport{Proxy: nil, DialContext: dialer.DialContext, MaxIdleConnsPerHost: 4, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 5 * time.Second}
	return &upstreamClients{h2: &http.Client{Transport: h2}, h1: &http.Client{Transport: h1}, h1Only: map[string]bool{}}
}

// order returns the clients to try for addr, preferred first.
func (u *upstreamClients) order(addr string) []*http.Client {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.h1Only[addr] {
		return []*http.Client{u.h1}
	}
	return []*http.Client{u.h2, u.h1}
}

// learn remembers that addr only answered HTTP/1.1 (or answers h2c again).
func (u *upstreamClients) learn(addr string, h1Only bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if h1Only {
		u.h1Only[addr] = true
	} else {
		delete(u.h1Only, addr)
	}
}

// backoff is the wait before reconnect attempt n (0-based): 1s doubling to
// a 15s cap, with ±50% jitter so many flows never reconnect in lockstep.
func backoff(attempt int) time.Duration {
	base := time.Second << min(attempt, 4)
	if base > 15*time.Second {
		base = 15 * time.Second
	}
	return base/2 + time.Duration(rand.Int64N(int64(base)))
}

// sleepOrKick waits d unless ctx ends or a kick arrives.
func sleepOrKick(ctx context.Context, d time.Duration, kick <-chan struct{}) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-kick:
		return true
	case <-timer.C:
		return true
	}
}

// upstreamSet keeps exactly one upstream per flow with at least one
// interested viewer, opening it lazily and closing it an idle grace after the
// last viewer leaves. The traffic and stats hubs are two sets.
type upstreamSet struct {
	kind string
	cfg  hubConfig
	// inventory lists every flow, for viewers that watch all of them.
	inventory func() []string
	// run serves one flow until ctx ends; kick asks it to re-resolve its
	// target now.
	run func(ctx context.Context, name string, kick <-chan struct{})

	mu         sync.Mutex
	all        int
	named      map[string]int
	ups        map[string]*upstream
	refresh    chan struct{}
	loopCancel context.CancelFunc
}

type upstream struct {
	cancel context.CancelFunc
	done   chan struct{}
	kick   chan struct{}
	idle   *time.Timer
}

func newUpstreamSet(kind string, cfg hubConfig, inventory func() []string, run func(context.Context, string, <-chan struct{})) *upstreamSet {
	return &upstreamSet{kind: kind, cfg: cfg, inventory: inventory, run: run, named: map[string]int{}, ups: map[string]*upstream{}, refresh: make(chan struct{}, 1)}
}

// want registers a viewer of flow ("" = every flow) and returns its release.
func (s *upstreamSet) want(flow string) (release func()) {
	s.mu.Lock()
	if flow == "" {
		s.all++
	} else {
		s.named[flow]++
	}
	if s.loopCancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		s.loopCancel = cancel
		go s.loop(ctx)
	}
	s.mu.Unlock()
	s.reconcile()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if flow == "" {
				s.all--
			} else if s.named[flow]--; s.named[flow] <= 0 {
				delete(s.named, flow)
			}
			s.mu.Unlock()
			s.reconcile()
		})
	}
}

// kick asks a flow's upstream to re-resolve its target now (a Pod changed).
func (s *upstreamSet) kick(flow string) {
	s.mu.Lock()
	up := s.ups[flow]
	s.mu.Unlock()
	if up != nil {
		select {
		case up.kick <- struct{}{}:
		default:
		}
	}
}

// Upstreams reports how many upstreams are open (including idle ones).
func (s *upstreamSet) Upstreams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ups)
}

func (s *upstreamSet) loop(ctx context.Context) {
	tick := time.NewTicker(s.cfg.resync)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.reconcile()
		case <-s.refresh:
			s.reconcile()
		}
	}
}

// reconcile opens upstreams for wanted flows and schedules idle ones to close.
func (s *upstreamSet) reconcile() {
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := map[string]bool{}
	for name := range s.named {
		wanted[name] = true
	}
	if s.all > 0 {
		for _, name := range s.inventory() {
			wanted[name] = true
		}
	}
	for name := range wanted {
		up, open := s.ups[name]
		if open {
			if up.idle != nil && up.idle.Stop() {
				up.idle = nil
			}
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		up = &upstream{cancel: cancel, done: make(chan struct{}), kick: make(chan struct{}, 1)}
		s.ups[name] = up
		observability.HubUpstream(s.kind, 1)
		go func() {
			defer close(up.done)
			s.run(ctx, name, up.kick)
		}()
	}
	for name, up := range s.ups {
		if wanted[name] || up.idle != nil {
			continue
		}
		up.idle = time.AfterFunc(s.cfg.idleGrace, func() { s.closeIdle(name, up) })
	}
	if len(s.ups) == 0 && s.all == 0 && len(s.named) == 0 && s.loopCancel != nil {
		s.loopCancel()
		s.loopCancel = nil
	}
}

func (s *upstreamSet) closeIdle(name string, up *upstream) {
	s.mu.Lock()
	if s.ups[name] != up || up.idle == nil {
		s.mu.Unlock()
		return
	}
	delete(s.ups, name)
	stopLoop := len(s.ups) == 0 && s.all == 0 && len(s.named) == 0 && s.loopCancel != nil
	if stopLoop {
		s.loopCancel()
		s.loopCancel = nil
	}
	s.mu.Unlock()
	up.cancel()
	<-up.done
	observability.HubUpstream(s.kind, -1)
}

// resolveLoop resolves a flow's runtime target and serves it until the
// session ends, then re-resolves after a jittered backoff. local and remote
// each serve one session and return when it ends; a target change (compared
// every resync, or pushed through kick) ends the session too.
func (s *upstreamSet) resolveLoop(ctx context.Context, ctrl *controller.Controller, name string, kick <-chan struct{},
	local func(ctx context.Context, target driver.TapTarget) error, remote func(ctx context.Context, target driver.TapTarget) error) {
	attempt := 0
	for ctx.Err() == nil {
		lookup, done := context.WithTimeout(ctx, 5*time.Second)
		target, found, err := ctrl.Tap(lookup, name)
		done()
		if err != nil || !found || (target.Local == nil && target.Addr == "") {
			if !sleepOrKick(ctx, backoff(attempt), kick) {
				return
			}
			attempt = min(attempt+1, 8)
			continue
		}
		sessionCtx, stop := context.WithCancel(ctx)
		changed := make(chan struct{})
		go s.watchTarget(sessionCtx, ctrl, name, target, kick, changed, stop)
		started := time.Now()
		if target.Local != nil {
			err = local(sessionCtx, target)
		} else {
			err = remote(sessionCtx, target)
		}
		stop()
		if ctx.Err() != nil {
			return
		}
		observability.RelayReconnect(s.kind)
		select {
		case <-changed:
			attempt = 0 // the target moved: reconnect right away
			continue
		default:
		}
		if err == nil || time.Since(started) > 30*time.Second {
			attempt = 0
		}
		if !sleepOrKick(ctx, backoff(attempt), kick) {
			return
		}
		attempt = min(attempt+1, 8)
	}
}

// watchTarget ends a session when the flow's target differs from the one it
// was opened with: on a kick (Pod watcher) and every resync.
func (s *upstreamSet) watchTarget(ctx context.Context, ctrl *controller.Controller, name string, current driver.TapTarget, kick <-chan struct{}, changed chan<- struct{}, stop context.CancelFunc) {
	tick := time.NewTicker(s.cfg.resync)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-tick.C:
		}
		lookup, done := context.WithTimeout(ctx, 5*time.Second)
		target, found, err := ctrl.Tap(lookup, name)
		done()
		if err != nil {
			continue
		}
		if !found || target.Local != current.Local || target.Addr != current.Addr {
			close(changed)
			stop()
			return
		}
	}
}
