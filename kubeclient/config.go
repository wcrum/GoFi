// Package kubeclient builds the one Kubernetes client configuration every
// GoFi component shares: in-cluster credentials, a client-side rate limit
// (GOFI_KUBE_QPS / GOFI_KUBE_BURST), a request timeout, and a user agent.
// client-go's request metrics (rate limiter wait, latency, results) are
// registered once, by the observability package (observability/clientgo.go),
// through client-go's default metrics hooks; this package adds nothing there.
//
// Before this package each component (driver, desired-state store, CEL
// library store, Python sandbox backend and store, history) built its own
// rest.Config with client-go's default limiter (5 QPS / 10 burst), so six
// uncoordinated limiters shared one service account and none of them was
// observable.
package kubeclient

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// DefaultQPS and DefaultBurst bound the controller's API traffic. A sync
	// of N flows is O(1) requests at steady state (informers serve reads), so
	// these mostly shape bursts after a restart or a mass edit.
	DefaultQPS   = 50
	DefaultBurst = 100
	// DefaultTimeout bounds every synchronous request. Watches use a config
	// without it (see WatchConfig) because the HTTP client timeout would cut
	// long-running watch streams.
	DefaultTimeout = 30 * time.Second
	// UserAgent identifies GoFi in API server audit logs.
	UserAgent = "gofi-controller"
)

// Clients are the shared typed and dynamic clients.
type Clients struct {
	// Config is the request config (rate limited, with Timeout).
	Config *rest.Config
	// WatchConfig is Config without the request timeout, for informers.
	WatchConfig *rest.Config
	Clientset   kubernetes.Interface
	Dynamic     dynamic.Interface
	// WatchClientset and WatchDynamic are built from WatchConfig: informers
	// use them so long-running watch streams are not cut by the timeout.
	WatchClientset kubernetes.Interface
	WatchDynamic   dynamic.Interface
}

var (
	once    sync.Once
	shared  *Clients
	initErr error
	// loader is replaced by tests; it yields the base in-cluster config.
	loader = rest.InClusterConfig
)

// Get returns the process-wide clients, building them on first use.
func Get() (*Clients, error) {
	once.Do(func() {
		base, err := loader()
		if err != nil {
			initErr = fmt.Errorf("kubernetes client: in-cluster config: %w", err)
			return
		}
		shared, initErr = NewClients(base)
	})
	return shared, initErr
}

// NewClients builds clients from base, tuned by Configure.
func NewClients(base *rest.Config) (*Clients, error) {
	cfg := Configure(rest.CopyConfig(base))
	watch := rest.CopyConfig(cfg)
	watch.Timeout = 0
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: dynamic client: %w", err)
	}
	watchCS, err := kubernetes.NewForConfig(watch)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: watch clientset: %w", err)
	}
	watchDyn, err := dynamic.NewForConfig(watch)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: watch dynamic client: %w", err)
	}
	return &Clients{Config: cfg, WatchConfig: watch, Clientset: cs, Dynamic: dyn, WatchClientset: watchCS, WatchDynamic: watchDyn}, nil
}

// Configure applies GoFi's rate limit, timeout, and user agent to cfg and
// returns it. Environment: GOFI_KUBE_QPS (float), GOFI_KUBE_BURST (int),
// GOFI_KUBE_TIMEOUT (Go duration).
func Configure(cfg *rest.Config) *rest.Config {
	cfg.QPS = float32(envFloat("GOFI_KUBE_QPS", DefaultQPS))
	cfg.Burst = envInt("GOFI_KUBE_BURST", DefaultBurst)
	cfg.Timeout = envDuration("GOFI_KUBE_TIMEOUT", DefaultTimeout)
	cfg.UserAgent = UserAgent
	return cfg
}

func envFloat(name string, fallback float64) float64 {
	if raw := os.Getenv(name); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

func envInt(name string, fallback int) int {
	if raw := os.Getenv(name); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	if raw := os.Getenv(name); raw != "" {
		if v, err := time.ParseDuration(raw); err == nil && v >= 0 {
			return v
		}
	}
	return fallback
}

// CallTimeout bounds one synchronous API call made with ctx. Every driver
// and store call site wraps its context with it so a stalled API server can
// never hold a reconcile (or the lock it runs under) indefinitely.
const CallTimeout = 15 * time.Second
