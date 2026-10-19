package spec

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ZenohSubscribe subscribes to a Zenoh key expression through the router's
// REST plugin (GET /<keyExpr> with Accept: text/event-stream). Each sample
// is one frame (docs/zenoh.md).
type ZenohSubscribe struct {
	URL     string            `json:"url"`     // router REST origin, e.g. http://zenoh-router:8000
	KeyExpr string            `json:"keyExpr"` // e.g. demo/sensors/** ; * and ** chunks allowed
	Headers map[string]string `json:"headers,omitempty"`
	Auth    *HTTPAuth         `json:"auth,omitempty"` // same shape as the HTTP and Zenoh sinks
	// Kinds selects sample kinds: PUT and/or DELETE. Default: PUT only.
	Kinds []string `json:"kinds,omitempty"`
	// Query also reads current values from the router's storages (GET
	// /<keyExpr> as JSON) each time the subscription (re)connects.
	Query bool `json:"query,omitempty"`
}

// Validate checks a Zenoh subscription without contacting the router.
func (z *ZenohSubscribe) Validate() error {
	if err := ValidateZenohOrigin(z.URL); err != nil {
		return fmt.Errorf("source.subscribe.zenoh.url: %w", err)
	}
	if _, err := ZenohKeyPath(z.KeyExpr, true); err != nil {
		return fmt.Errorf("source.subscribe.zenoh.keyExpr: %w", err)
	}
	for _, kind := range z.Kinds {
		if kind != "PUT" && kind != "DELETE" {
			return fmt.Errorf("source.subscribe.zenoh.kinds may contain only PUT and DELETE")
		}
	}
	for name, value := range z.Headers {
		if name == "" || strings.ContainsAny(name, " \t\r\n:") || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("source.subscribe.zenoh.headers: invalid header")
		}
	}
	if z.Auth != nil {
		switch z.Auth.Type {
		case "bearer", "basic", "apiKey":
		default:
			return fmt.Errorf("source.subscribe.zenoh.auth.type must be bearer, basic, or apiKey")
		}
	}
	return nil
}

// AcceptsKind reports whether the subscription forwards samples of kind
// ("PUT" or "DELETE"); an empty Kinds list accepts PUT only.
func (z *ZenohSubscribe) AcceptsKind(kind string) bool {
	if len(z.Kinds) == 0 {
		return kind == "PUT"
	}
	for _, k := range z.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// ValidateZenohOrigin requires an absolute http(s) router origin: no path,
// query, fragment, or URL credentials (supply credentials with auth).
func ValidateZenohOrigin(raw string) error {
	origin, err := url.Parse(raw)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Hostname() == "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		return fmt.Errorf("must be an absolute HTTP or HTTPS router origin without a path, query, fragment, or credentials")
	}
	return nil
}

// ZenohKeyPath validates a Zenoh key (or, with wildcards, a key expression
// whose chunks may be exactly * or **) and returns it as an escaped URL
// path without the leading slash. It rejects the admin space (@), verbatim
// chunks, $* partial wildcards, query/selector syntax, encoded paths,
// control characters, and empty or traversal segments, so a request can
// only address the configured keys.
func ZenohKeyPath(key string, wildcards bool) (string, error) {
	if key == "" || !utf8.ValidString(key) {
		return "", fmt.Errorf("key is required")
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("key contains a control character")
		}
	}
	segments := strings.Split(key, "/")
	escaped := make([]string, len(segments))
	for i, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("key segments must be nonempty and cannot be . or ..")
		}
		if segment == "*" || segment == "**" {
			if !wildcards {
				return "", fmt.Errorf("must be a concrete key without wildcards")
			}
			if segment == "**" && i > 0 && segments[i-1] == "**" {
				return "", fmt.Errorf("**/** is not a canonical key expression; use **")
			}
			escaped[i] = segment
			continue
		}
		if strings.ContainsAny(segment, "*$?#@\\%") {
			if wildcards {
				return "", fmt.Errorf("wildcards must be whole * or ** segments; query syntax and admin paths (@) are not allowed")
			}
			return "", fmt.Errorf("must be a concrete key without wildcards, query syntax, or admin paths")
		}
		escaped[i] = url.PathEscape(segment)
	}
	return strings.Join(escaped, "/"), nil
}
