package sink

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nats-io/nkeys"
	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/spec"
)

// Validate checks configuration without dialing destinations or reading mounted
// credentials. A controller and its listener may run in different containers.
func Validate(cfg spec.SinkCfg) error {
	if cfg.When != "" {
		if _, err := filter.CompilePredicate(cfg.When); err != nil {
			return fmt.Errorf("sink when: %w", err)
		}
	}
	if cfg.Kubernetes != nil {
		if cfg.Type != "tcp" && cfg.Type != "udp" {
			return fmt.Errorf("Kubernetes targets support only tcp or udp sinks")
		}
		if cfg.URL != "" {
			return fmt.Errorf("choose URL or Kubernetes selector target")
		}
		if err := cfg.Kubernetes.Validate(); err != nil {
			return err
		}
		// Construct only a syntactic URL for the shared static network checks.
		cfg.URL = fmt.Sprintf("%s://validation.invalid:%d", cfg.Type, cfg.Kubernetes.Port)
		cfg.Kubernetes = nil
	}
	switch cfg.Type {
	case "python":
		return cfg.Python.Validate()
	case "", "stdout":
		return nil
	case "tcp", "udp":
		_, err := newNetwork(cfg, "validation")
		return err
	case "aster":
		_, err := configureAster(cfg)
		return err
	case "loki":
		return ValidateLoki(cfg)
	case "zenoh":
		normalized, err := zenohHTTPConfig(cfg)
		if err != nil {
			return err
		}
		return Validate(normalized)
	case "http":
		s, err := newHTTP(cfg)
		if err != nil {
			return err
		}
		u, err := url.Parse(s.url)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return fmt.Errorf("http sink: URL must be an absolute HTTP or HTTPS URL")
		}
		if u.Port() != "" {
			port, err := strconv.Atoi(u.Port())
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("http sink: invalid port")
			}
		}
		req, err := http.NewRequest(s.method, s.url, nil)
		if err != nil {
			return fmt.Errorf("http sink: invalid request URL")
		}
		req.Header.Set("Content-Type", s.contentType)
		for k, v := range cfg.Headers {
			if !validHeaderName(k) || strings.ContainsAny(v, "\r\n") {
				return fmt.Errorf("http sink: invalid header")
			}
		}
		if cfg.Auth != nil && cfg.Auth.Type == "apiKey" && cfg.Auth.HeaderName != "" && !validHeaderName(cfg.Auth.HeaderName) {
			return fmt.Errorf("http sink: invalid API key header name")
		}
		if strings.ContainsAny(cfg.ContentType, "\r\n") || (cfg.Auth != nil && (strings.ContainsAny(cfg.Auth.Token, "\r\n") || strings.ContainsAny(cfg.Auth.APIKey, "\r\n"))) {
			return fmt.Errorf("http sink: invalid header value")
		}
		return nil
	case "nats":
		for _, server := range strings.Split(cfg.URL, ",") {
			if cfg.URL == "" {
				break // the local NATS default is supported
			}
			u, err := url.Parse(strings.TrimSpace(server))
			if err != nil || u.Hostname() == "" || (u.Scheme != "nats" && u.Scheme != "tls" && u.Scheme != "ws" && u.Scheme != "wss") {
				return fmt.Errorf("nats sink: invalid server URL")
			}
			if u.Port() != "" {
				port, err := strconv.Atoi(u.Port())
				if err != nil || port < 1 || port > 65535 {
					return fmt.Errorf("nats sink: invalid server port")
				}
			}
		}
		if strings.ContainsAny(cfg.Subject, " \t\r\n*>") || strings.HasPrefix(cfg.Subject, ".") || strings.HasSuffix(cfg.Subject, ".") || strings.Contains(cfg.Subject, "..") {
			return fmt.Errorf("nats sink: invalid publish subject")
		}
		if cfg.NATSAuth != nil {
			a := cfg.NATSAuth
			if (a.TLSCert == "") != (a.TLSKey == "") {
				return fmt.Errorf("nats sink: TLS certificate and key must be specified together")
			}
			if a.Creds != "" {
				if _, err := nkeys.ParseDecoratedJWT([]byte(a.Creds)); err != nil {
					return fmt.Errorf("nats sink: invalid inline credentials")
				}
				key, err := nkeys.ParseDecoratedUserNKey([]byte(a.Creds))
				if err != nil {
					return fmt.Errorf("nats sink: invalid inline credential seed")
				}
				key.Wipe()
			}
		}
		_, err := natsAuthOptions(cfg.NATSAuth)
		return err
	default:
		return fmt.Errorf("sink: unknown type %q", cfg.Type)
	}
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}
