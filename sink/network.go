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
	fanout := false
	if cfg.Kubernetes != nil {
		if err := cfg.Kubernetes.Validate(); err != nil {
			return nil, err
		}
		if cfg.URL != "" {
			return nil, fmt.Errorf("choose URL or Kubernetes selector target")
		}
		namespace := os.Getenv("GOFI_K8S_NAMESPACE")
		if namespace == "" {
			return nil, fmt.Errorf("Kubernetes target namespace is required in the listener environment")
		}
		// The target Service always lives in GoFi's namespace; for Pods in
		// another namespace GoFi publishes their addresses behind it.
		fanout = cfg.Kubernetes.FanOut()
		host := spec.KubernetesServiceName(listenerName, cfg.Type, cfg.Kubernetes) + "." + namespace + ".svc.cluster.local"
		cfg.URL = cfg.Type + "://" + net.JoinHostPort(host, strconv.Itoa(cfg.Kubernetes.Port))
	}

	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != cfg.Type || u.Hostname() == "" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("%s sink: URL must be %s://host:port", cfg.Type, cfg.Type)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("%s sink: invalid port", cfg.Type)
	}
	mode := cfg.BodyMode
	if mode == "" {
		mode = "raw"
	}
	if mode != "raw" && mode != "wrapped" {
		return nil, fmt.Errorf("%s sink: unsupported bodyMode %q", cfg.Type, mode)
	}
	if fanout {
		return newFanout(cfg.Type, u.Hostname(), u.Port(), func(address string) *networkSink {
			return &networkSink{network: cfg.Type, address: address, delimiter: cfg.Delimiter, bodyMode: mode}
		}), nil
	}
	return &networkSink{network: cfg.Type, address: u.Host, delimiter: cfg.Delimiter, bodyMode: mode}, nil
}

func (s *networkSink) Send(msg *message.Message) error {
	payload := msg.Raw
	if msg.Payload != nil {
		payload = msg.Payload
	} else if s.bodyMode == "wrapped" {
		var err error
		payload, err = json.Marshal(map[string]any{"fields": msg.Fields, "meta": msg.Meta})
		if err != nil {
			return err
		}
	}
	payload = append(append([]byte(nil), payload...), s.delimiter...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		conn, err := net.DialTimeout(s.network, s.address, 5*time.Second)
		if err != nil {
			return fmt.Errorf("%s sink: dial: %w", s.network, err)
		}
		s.conn = conn
	}
	if err := s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if s.network == "udp" {
		_, err := s.conn.Write(payload)
		if err != nil {
			s.conn.Close()
			s.conn = nil
			return fmt.Errorf("udp sink: write: %w", err)
		}
		return nil
	}
	for len(payload) > 0 {
		n, err := s.conn.Write(payload)
		if err != nil {
			s.conn.Close()
			s.conn = nil
			// Do not retry a partially written message: delivery may already
			// have occurred. The next message opens a fresh connection.
			return fmt.Errorf("%s sink: write: %w", s.network, err)
		}
		if n == 0 {
			s.conn.Close()
			s.conn = nil
			return fmt.Errorf("%s sink: zero-byte write", s.network)
		}
		payload = payload[n:]
	}
	return nil
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
