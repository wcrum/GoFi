package sink

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

type networkSink struct {
	network, address, delimiter, bodyMode string
	mu                                    sync.Mutex
	conn                                  net.Conn
}

func newNetwork(cfg spec.SinkCfg, listenerName string) (Sink, error) {
	cfg, fanout, err := kubernetesTargetURL(cfg, listenerName)
	if err != nil {
		return nil, err
	}
	u, mode, err := networkEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	if fanout {
		return newFanout(cfg.Type, u.Hostname(), u.Port(), func(address string) Sink {
			return &networkSink{network: cfg.Type, address: address, delimiter: cfg.Delimiter, bodyMode: mode}
		}), nil
	}
	return &networkSink{network: cfg.Type, address: u.Host, delimiter: cfg.Delimiter, bodyMode: mode}, nil
}

// kubernetesTargetURL addresses a Kubernetes selector target (tcp, udp, quic
// sinks): it returns cfg with URL set to the target Service GoFi publishes
// in its own namespace, and whether every ready Pod behind it must receive
// each message (fan-out). cfg is returned unchanged without a target.
func kubernetesTargetURL(cfg spec.SinkCfg, listenerName string) (spec.SinkCfg, bool, error) {
	if cfg.Kubernetes == nil {
		return cfg, false, nil
	}
	if err := cfg.Kubernetes.Validate(); err != nil {
		return cfg, false, err
	}
	if cfg.URL != "" {
		return cfg, false, fmt.Errorf("choose URL or Kubernetes selector target")
	}
	namespace := os.Getenv("GOFI_K8S_NAMESPACE")
	if namespace == "" {
		return cfg, false, fmt.Errorf("Kubernetes target namespace is required in the listener environment")
	}
	// The target Service always lives in GoFi's namespace; for Pods in
	// another namespace GoFi publishes their addresses behind it.
	host := spec.KubernetesServiceName(listenerName, cfg.Type, cfg.Kubernetes) + "." + namespace + ".svc.cluster.local"
	cfg.URL = cfg.Type + "://" + net.JoinHostPort(host, strconv.Itoa(cfg.Kubernetes.Port))
	return cfg, cfg.Kubernetes.FanOut(), nil
}

// networkEndpoint checks a <type>://host:port sink URL (tcp, udp, quic) and
// the body mode shared by the socket sinks.
func networkEndpoint(cfg spec.SinkCfg) (*url.URL, string, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != cfg.Type || u.Hostname() == "" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, "", fmt.Errorf("%s sink: URL must be %s://host:port", cfg.Type, cfg.Type)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, "", fmt.Errorf("%s sink: invalid port", cfg.Type)
	}
	mode := cfg.BodyMode
	if mode == "" {
		mode = "raw"
	}
	if mode != "raw" && mode != "wrapped" {
		return nil, "", fmt.Errorf("%s sink: unsupported bodyMode %q", cfg.Type, mode)
	}
	return u, mode, nil
}

// socketPayload is the bytes a socket sink writes for msg: an encoding's
// output, the raw frame, or the wrapped fields+meta document.
func socketPayload(msg *message.Message, bodyMode string) ([]byte, error) {
	if msg.Payload != nil {
		return msg.Payload, nil
	}
	if bodyMode == "wrapped" {
		return json.Marshal(map[string]any{"fields": msg.Fields, "meta": msg.Meta})
	}
	return msg.Raw, nil
}

// networkWriteTimeout bounds one write (and the dial before it).
const networkWriteTimeout = 5 * time.Second

// Send writes the payload and delimiter as one vectored write (net.Buffers),
// without copying them into a joined buffer. The connection is dialed
// outside the lock; TCP writes hold it so messages never interleave, UDP
// writes (one datagram each) do not.
func (s *networkSink) Send(msg *message.Message) error {
	payload, err := socketPayload(msg, s.bodyMode)
	if err != nil {
		return err
	}
	conn, err := s.connection()
	if err != nil {
		return err
	}
	if s.network == "udp" {
		if len(s.delimiter) > 0 {
			// One datagram: the kernel does not coalesce vectored UDP writes
			// across calls, so join for the (rare) delimited UDP sink.
			payload = append(append(make([]byte, 0, len(payload)+len(s.delimiter)), payload...), s.delimiter...)
		}
		_ = conn.SetWriteDeadline(time.Now().Add(networkWriteTimeout))
		if _, err := conn.Write(payload); err != nil {
			s.drop(conn)
			return fmt.Errorf("udp sink: write: %w", err)
		}
		return nil
	}
	bufs := net.Buffers{payload}
	if len(s.delimiter) > 0 {
		bufs = append(bufs, []byte(s.delimiter))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != conn {
		// Replaced by a concurrent failure; use the live one.
		if s.conn == nil {
			return fmt.Errorf("%s sink: connection lost", s.network)
		}
		conn = s.conn
	}
	if err := conn.SetWriteDeadline(time.Now().Add(networkWriteTimeout)); err != nil {
		return err
	}
	// WriteTo on net.Buffers writes everything or returns an error.
	if _, err := bufs.WriteTo(conn); err != nil {
		conn.Close()
		s.conn = nil
		// Do not retry a partially written message: delivery may already
		// have occurred. The next message opens a fresh connection.
		return fmt.Errorf("%s sink: write: %w", s.network, err)
	}
	return nil
}

// connection returns the open connection, dialing one when needed. Dialing
// happens outside the write lock so a slow dial does not hold other
// senders, but only one dial proceeds at a time.
func (s *networkSink) connection() (net.Conn, error) {
	s.mu.Lock()
	if s.conn != nil {
		conn := s.conn
		s.mu.Unlock()
		return conn, nil
	}
	s.mu.Unlock()
	conn, err := net.DialTimeout(s.network, s.address, networkWriteTimeout)
	if err != nil {
		return nil, fmt.Errorf("%s sink: dial: %w", s.network, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		conn.Close() // another sender dialed first
		return s.conn, nil
	}
	s.conn = conn
	return conn, nil
}

// drop closes conn and forgets it if it is still the current one.
func (s *networkSink) drop(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conn.Close()
	if s.conn == conn {
		s.conn = nil
	}
}

func (s *networkSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}
