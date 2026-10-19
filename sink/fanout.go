package sink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/wcrum/gofi/message"
)

// fanoutRefresh bounds how stale the Pod address set may be.
const fanoutRefresh = 5 * time.Second

// fanoutSink sends each message to every address a headless Service name
// resolves to (one per ready Pod), keeping one peer sink (connection) per
// Pod. Peers are tcp/udp networkSinks or quicSinks.
type fanoutSink struct {
	network, host, port string
	newPeer             func(address string) Sink
	lookup              func(ctx context.Context, host string) ([]string, error)

	mu       sync.Mutex
	peers    map[string]Sink
	resolved time.Time
}

func newFanout(network, host, port string, newPeer func(string) Sink) *fanoutSink {
	return &fanoutSink{network: network, host: host, port: port, newPeer: newPeer, lookup: net.DefaultResolver.LookupHost, peers: map[string]Sink{}}
}

// targets refreshes the address set from DNS at most every fanoutRefresh,
// closing connections to Pods that went away.
func (s *fanoutSink) targets() ([]Sink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.resolved) >= fanoutRefresh || len(s.peers) == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		addresses, err := s.lookup(ctx, s.host)
		cancel()
		if err != nil && len(s.peers) == 0 {
			return nil, fmt.Errorf("%s sink: no ready Pods for %s: %w", s.network, s.host, err)
		}
		if err == nil {
			s.resolved = time.Now()
			wanted := map[string]bool{}
			for _, address := range addresses {
				key := net.JoinHostPort(address, s.port)
				wanted[key] = true
				if s.peers[key] == nil {
					s.peers[key] = s.newPeer(key)
				}
			}
			for key, peer := range s.peers {
				if !wanted[key] {
					_ = peer.Close()
					delete(s.peers, key)
				}
			}
		}
	}
	keys := make([]string, 0, len(s.peers))
	for key := range s.peers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Sink, 0, len(keys))
	for _, key := range keys {
		out = append(out, s.peers[key])
	}
	return out, nil
}

// Send delivers to every Pod. It fails only if no Pod received the message:
// a partial failure is logged, not returned, so the message is not
// dead-lettered or retried for Pods that already have it.
func (s *fanoutSink) Send(msg *message.Message) error {
	peers, err := s.targets()
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		return fmt.Errorf("%s sink: no ready Pods for %s", s.network, s.host)
	}
	var failures []error
	for _, peer := range peers {
		if err := peer.Send(msg); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) == len(peers) {
		return errors.Join(failures...)
	}
	if len(failures) > 0 {
		slog.Warn("fan-out sink: partial delivery", "network", s.network, "target", s.host, "delivered", len(peers)-len(failures), "pods", len(peers), "error", errors.Join(failures...))
	}
	return nil
}

func (s *fanoutSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for key, peer := range s.peers {
		errs = append(errs, peer.Close())
		delete(s.peers, key)
	}
	return errors.Join(errs...)
}
