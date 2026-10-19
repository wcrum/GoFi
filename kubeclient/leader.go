package kubeclient

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/wcrum/gofi/observability"
)

// Leader election lets more than one controller replica run: only the
// holder of the Lease reconciles flows and supervises Python workers; the
// others serve the portal and the read API and refuse writes with 503 until
// they win.
//
// Environment (rendered by the chart): GOFI_LEADER_ELECTION=true enables it;
// GOFI_LEADER_ELECTION_LEASE names the Lease (default gofi-controller);
// GOFI_LEADER_ELECTION_LEASE_DURATION, _RENEW_DEADLINE, _RETRY_PERIOD are Go
// durations (defaults 15s, 10s, 2s); GOFI_POD_NAME is the holder identity.
//
// RBAC: the controller's Role needs coordination.k8s.io/leases with get,
// list, watch, create, update, patch (the chart's rbac.yaml).

// DefaultLeaseName is the Lease the replicas compete for when the chart
// does not name one.
const DefaultLeaseName = "gofi-controller"

// ElectionConfig is read from the environment by ElectionFromEnv.
type ElectionConfig struct {
	Enabled       bool
	LeaseName     string
	Identity      string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
}

// ElectionFromEnv reads the chart's leader election settings.
func ElectionFromEnv() ElectionConfig {
	cfg := ElectionConfig{
		Enabled:       EnvBool("GOFI_LEADER_ELECTION", false),
		LeaseName:     os.Getenv("GOFI_LEADER_ELECTION_LEASE"),
		Identity:      os.Getenv("GOFI_POD_NAME"),
		LeaseDuration: envDuration("GOFI_LEADER_ELECTION_LEASE_DURATION", 15*time.Second),
		RenewDeadline: envDuration("GOFI_LEADER_ELECTION_RENEW_DEADLINE", 10*time.Second),
		RetryPeriod:   envDuration("GOFI_LEADER_ELECTION_RETRY_PERIOD", 2*time.Second),
	}
	if cfg.LeaseName == "" {
		cfg.LeaseName = DefaultLeaseName
	}
	if cfg.Identity == "" {
		cfg.Identity, _ = os.Hostname()
	}
	return cfg
}

// Election is the state of this replica's participation.
type Election struct {
	leading  atomic.Bool
	identity string
	holder   atomic.Pointer[string]
}

// IsLeader reports whether this replica holds the Lease right now.
func (e *Election) IsLeader() bool { return e.leading.Load() }

// Identity is this replica's name in the Lease (its Pod name).
func (e *Election) Identity() string { return e.identity }

// Holder is the identity of the current leader as last observed ("" when
// unknown).
func (e *Election) Holder() string {
	if h := e.holder.Load(); h != nil {
		return *h
	}
	return ""
}

// LeaderCallbacks run on the transitions of this replica.
type LeaderCallbacks struct {
	// OnStartedLeading runs in its own goroutine with a context that ends
	// when leadership is lost. All reconciling work starts here.
	OnStartedLeading func(ctx context.Context)
	// OnStoppedLeading runs after leadership is lost (or ctx ended). A lost
	// lease means another replica may already be reconciling: the standard
	// response is to exit so the Pod restarts as a clean follower.
	OnStoppedLeading func()
}

// RunElection competes for the Lease in namespace until ctx ends. It returns
// the Election immediately; the campaign runs in the background.
func RunElection(ctx context.Context, cs kubernetes.Interface, namespace string, cfg ElectionConfig, callbacks LeaderCallbacks) (*Election, error) {
	if cfg.Identity == "" {
		return nil, fmt.Errorf("leader election: no identity (set GOFI_POD_NAME or a hostname)")
	}
	if cfg.LeaseName == "" {
		cfg.LeaseName = DefaultLeaseName
	}
	if cfg.LeaseDuration <= 0 || cfg.RenewDeadline <= 0 || cfg.RetryPeriod <= 0 || cfg.RenewDeadline >= cfg.LeaseDuration || cfg.RetryPeriod >= cfg.RenewDeadline {
		return nil, fmt.Errorf("leader election: need retryPeriod < renewDeadline < leaseDuration (got %s, %s, %s)", cfg.RetryPeriod, cfg.RenewDeadline, cfg.LeaseDuration)
	}
	e := &Election{identity: cfg.Identity}
	observability.SetLeader(cfg.Identity, false)
	lock := &resourcelock.LeaseLock{
		LeaseMeta:  metav1.ObjectMeta{Name: cfg.LeaseName, Namespace: namespace},
		Client:     cs.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{Identity: cfg.Identity},
	}
	config := leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   cfg.LeaseDuration,
		RenewDeadline:   cfg.RenewDeadline,
		RetryPeriod:     cfg.RetryPeriod,
		ReleaseOnCancel: true,
		Name:            cfg.LeaseName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leadCtx context.Context) {
				e.leading.Store(true)
				observability.SetLeader(cfg.Identity, true)
				observability.LeaderTransition()
				slog.Info("leader election: leading", "identity", cfg.Identity, "lease", cfg.LeaseName)
				if callbacks.OnStartedLeading != nil {
					callbacks.OnStartedLeading(leadCtx)
				}
			},
			OnStoppedLeading: func() {
				wasLeading := e.leading.Swap(false)
				observability.SetLeader(cfg.Identity, false)
				if wasLeading {
					observability.LeaderTransition()
					slog.Warn("leader election: leadership lost", "identity", cfg.Identity)
				}
				if callbacks.OnStoppedLeading != nil {
					callbacks.OnStoppedLeading()
				}
			},
			OnNewLeader: func(holder string) {
				e.holder.Store(&holder)
				if holder != cfg.Identity {
					slog.Info("leader election: following", "leader", holder, "identity", cfg.Identity)
				}
			},
		},
	}
	elector, err := leaderelection.NewLeaderElector(config)
	if err != nil {
		return nil, fmt.Errorf("leader election: %w", err)
	}
	go func() {
		for ctx.Err() == nil {
			// Run returns when leadership is lost or ctx ends; a replica that
			// lost the lease campaigns again unless OnStoppedLeading exited.
			elector.Run(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(cfg.RetryPeriod):
			}
		}
	}()
	return e, nil
}
