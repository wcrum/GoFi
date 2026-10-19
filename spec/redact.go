package spec

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Redacted is the sentinel that stands in for a credential in API read
// responses (ListFlows, GetFlow, revision reads). A definition saved with
// this value in a secret-bearing field keeps the stored value there
// (MergeRedacted), so a client can round-trip what it was shown.
const Redacted = "***"

// secretKeys are JSON keys whose string values are credentials wherever they
// appear in a definition: HTTP auth, NATS auth, Kafka SASL, broker and SQL
// passwords, S3 credentials, Aster tokens, and per-node Python configuration
// (python.config keys are the function's keyword names, so api_key covers an
// LLM key given to a library function).
var secretKeys = map[string]bool{
	"password":        true,
	"token":           true,
	"apiKey":          true,
	"api_key":         true,
	"secretAccessKey": true,
	"sessionToken":    true,
	"dsn":             true,
	"nkeySeed":        true,
	"creds":           true,
	"tlsKey":          true,
}

// secretHeaders are header names (lower-case) whose values carry credentials
// in a headers map (HTTP sink, HTTP fetch, WebSocket/SSE/Zenoh sources).
var secretHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"x-api-key":           true,
	"api-key":             true,
	"x-auth-token":        true,
	"cookie":              true,
}

// isSecret reports whether the string at key, inside the object stored
// under parent, is a credential.
func isSecret(parent, key string) bool {
	if secretKeys[key] {
		return true
	}
	if key == "key" && parent == "tls" { // TLSConfig.key, SourceTLS.key: PEM private keys
		return true
	}
	return parent == "headers" && secretHeaders[strings.ToLower(key)]
}

// Redact returns a copy of def with every credential replaced by Redacted:
// inline TLS private keys (source and sink tls.key, Kafka/AMQP/Redis tls.key,
// NATS tlsKey), passwords, bearer tokens and API keys, S3 secret and session
// keys, SQL DSNs, NKey seeds, .creds files, Aster tokens, credential headers,
// and the password part of a URL's userinfo. Everything else is unchanged.
// Nothing stored or run is redacted: this is for what people are shown.
func Redact(def FlowDefinition) FlowDefinition {
	tree, err := toTree(def)
	if err != nil {
		return def
	}
	redactTree(tree, "")
	out, err := fromTree(tree)
	if err != nil {
		return def
	}
	return out
}

// HasRedacted reports whether def carries the sentinel anywhere a credential
// could be (the shape a client sends back after reading a redacted flow).
func HasRedacted(def FlowDefinition) bool {
	tree, err := toTree(def)
	if err != nil {
		return false
	}
	found := false
	walk(tree, nil, func(path []string, value string) bool {
		if value == Redacted || urlPassword(value) == Redacted {
			found = true
		}
		return false
	})
	return found
}

// MergeRedacted returns incoming with every Redacted sentinel replaced by the
// value at the same position in stored (the flow as the store holds it), so
// saving a redacted read back changes nothing but what the client edited.
// Positions are structural: the second sink's password is restored from the
// stored second sink. A sentinel with no stored value behind it is an error.
func MergeRedacted(incoming, stored FlowDefinition) (FlowDefinition, error) {
	tree, err := toTree(incoming)
	if err != nil {
		return incoming, err
	}
	storedTree, err := toTree(stored)
	if err != nil {
		return incoming, err
	}
	var missing []string
	walk(tree, nil, func(path []string, value string) bool {
		switch {
		case value == Redacted:
			if kept, ok := lookup(storedTree, path).(string); ok && kept != "" && kept != Redacted {
				setAt(tree, path, kept)
				return true
			}
		case urlPassword(value) == Redacted:
			if kept, ok := lookup(storedTree, path).(string); ok {
				if merged, ok := mergeURLPassword(value, kept); ok {
					setAt(tree, path, merged)
					return true
				}
			}
		default:
			return false
		}
		missing = append(missing, strings.Join(path, "."))
		return false
	})
	if len(missing) > 0 {
		return incoming, fmt.Errorf("%s: value is %s (redacted) but the stored flow has no value to keep; set the credential", strings.Join(missing, ", "), Redacted)
	}
	return fromTree(tree)
}

func toTree(def FlowDefinition) (any, error) {
	data, err := json.Marshal(def)
	if err != nil {
		return nil, err
	}
	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, err
	}
	return tree, nil
}

func fromTree(tree any) (FlowDefinition, error) {
	data, err := json.Marshal(tree)
	if err != nil {
		return FlowDefinition{}, err
	}
	var out FlowDefinition
	err = json.Unmarshal(data, &out)
	return out, err
}

// redactTree replaces credentials in place. key is the JSON key the node
// sits under (array items inherit their array's key).
func redactTree(node any, key string) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if s, ok := child.(string); ok {
				switch {
				case s == "":
				case isSecret(key, k):
					v[k] = Redacted
				case k == "url":
					v[k] = redactURL(s)
				}
				continue
			}
			redactTree(child, k)
		}
	case []any:
		for _, child := range v {
			redactTree(child, key)
		}
	}
}

// walk visits every string in the tree with its path; visit returns whether
// it replaced the value (informational).
func walk(node any, path []string, visit func(path []string, value string) bool) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			walk(child, append(path[:len(path):len(path)], k), visit)
		}
	case []any:
		for i, child := range v {
			walk(child, append(path[:len(path):len(path)], strconv.Itoa(i)), visit)
		}
	case string:
		visit(path, v)
	}
}

func lookup(node any, path []string) any {
	for _, step := range path {
		switch v := node.(type) {
		case map[string]any:
			node = v[step]
		case []any:
			i, err := strconv.Atoi(step)
			if err != nil || i < 0 || i >= len(v) {
				return nil
			}
			node = v[i]
		default:
			return nil
		}
	}
	return node
}

func setAt(node any, path []string, value string) {
	if len(path) == 0 {
		return
	}
	parent := lookup(node, path[:len(path)-1])
	last := path[len(path)-1]
	switch v := parent.(type) {
	case map[string]any:
		v[last] = value
	case []any:
		if i, err := strconv.Atoi(last); err == nil && i >= 0 && i < len(v) {
			v[i] = value
		}
	}
}

// passwordSpan locates the password inside a URL's userinfo
// (scheme://user:password@host...) as raw text, so redaction and merging
// never re-encode the rest of the URL.
func passwordSpan(raw string) (start, end int, ok bool) {
	scheme := strings.Index(raw, "://")
	if scheme < 0 {
		return 0, 0, false
	}
	rest := raw[scheme+3:]
	at := strings.IndexByte(rest, '@')
	if at < 0 {
		return 0, 0, false
	}
	if cut := strings.IndexAny(rest, "/?#"); cut >= 0 && cut < at {
		return 0, 0, false // the @ is in the path or query, not userinfo
	}
	colon := strings.IndexByte(rest[:at], ':')
	if colon < 0 {
		return 0, 0, false
	}
	start, end = scheme+3+colon+1, scheme+3+at
	return start, end, start < end
}

// urlPassword returns the password in a URL's userinfo, or "".
func urlPassword(raw string) string {
	start, end, ok := passwordSpan(raw)
	if !ok {
		return ""
	}
	return raw[start:end]
}

// redactURL replaces the password in a URL's userinfo with Redacted.
func redactURL(raw string) string {
	start, end, ok := passwordSpan(raw)
	if !ok {
		return raw
	}
	return raw[:start] + Redacted + raw[end:]
}

// mergeURLPassword puts stored's userinfo password into incoming (which
// carries the sentinel). ok is false when stored has none.
func mergeURLPassword(incoming, stored string) (string, bool) {
	kept := urlPassword(stored)
	if kept == "" || kept == Redacted {
		return "", false
	}
	start, end, ok := passwordSpan(incoming)
	if !ok {
		return "", false
	}
	return incoming[:start] + kept + incoming[end:], true
}
