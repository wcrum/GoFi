package spec

// ListenerDefinition describes a full ingest pipeline:
// a network source → framer → parser → ordered stage chain → sink(s).
type ListenerDefinition struct {
	Name           string      `json:"name"`
	Source         Source      `json:"source"`
	Framing        Framing     `json:"framing"`
	Parser         ParserCfg   `json:"parser"`
	Stages         []StageSpec `json:"stages,omitempty"`
	Sinks          []SinkCfg   `json:"sinks,omitempty"`
	DeadLetterSink *SinkCfg    `json:"deadLetterSink,omitempty"` // optional; receives messages dropped by the stage chain
	TestCases      []TestCase  `json:"testCases,omitempty"`
}

// TestCase is a saved mock-mode sample input for exercising this listener's
// stage chain without real traffic. Purely descriptive: the runtime
// (listener/pipeline packages) never reads this field — it exists solely for
// the editor's mock-mode panel and pipeline.Simulate.
type TestCase struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Raw  string `json:"raw"` // raw frame text fed through Simulate
}

// Source defines the network endpoint to listen on.
type Source struct {
	Port    int    `json:"port"`
	Network string `json:"network"` // "tcp" | "udp"
}

// Framing controls how the raw byte stream is split into discrete frames.
type Framing struct {
	// Mode is one of: "line", "delimited", "length-prefix", "raw".
	//   line          — split on \n
	//   delimited     — accumulate until Delimiter byte sequence is found
	//   length-prefix — read a big-endian uint16 or uint32 length header, then that many bytes
	//   raw           — entire connection is one frame (closed on disconnect)
	Mode      string `json:"mode"`
	Delimiter string `json:"delimiter,omitempty"` // used by "delimited"
	PrefixLen int    `json:"prefixLen,omitempty"` // 2 or 4; used by "length-prefix"
	MaxBytes  int    `json:"maxBytes,omitempty"`  // safety cap; 0 = 1 MiB
}

// ParserCfg selects how a frame's bytes are decoded into Message.Fields.
type ParserCfg struct {
	// Format is one of: "xml", "json", "none".
	//   xml  — top-level attributes and first-level child text nodes become Fields
	//   json — top-level keys become Fields
	//   none — Fields["raw"] = frame as UTF-8 string
	Format string `json:"format"`
}

// StageSpec configures one processing block in the ordered chain between
// ingress and the sink(s). Stages run in array order. Kind selects behavior:
//
//	"filter"     — evaluate Expression (Type-specific, "cel" today); false drops the message
//	"transform"  — mutate the message in place; never drops (aside from an eval error, which fails open)
//	"rate-limit" — drop when the listener-wide rate exceeds MaxPerSecond (approximate per-second window, shared across all connections of this listener)
//	"dedup"      — drop repeats of KeyExpr's value seen within Window (shared across all connections of this listener)
//	"schema"     — drop messages missing any entry in RequiredFields
//
// Messages that are dropped by any stage short-circuit the remaining chain.
type StageSpec struct {
	ID   string `json:"id"`   // client-generated, stable, unique in chain
	Kind string `json:"kind"` // "filter" | "transform" | "rate-limit" | "dedup" | "schema"

	// Kind == "filter"
	Type       string `json:"type,omitempty"`       // "cel" (only type today); no default — empty/unknown = error
	Expression string `json:"expression,omitempty"` // cel-specific

	// Kind == "transform"
	TransformOp string `json:"transformOp,omitempty"` // "set" | "rename" | "remove"
	Field       string `json:"field,omitempty"`       // target field (all ops)
	NewField    string `json:"newField,omitempty"`    // "rename": destination field name
	ValueExpr   string `json:"valueExpr,omitempty"`   // "set": CEL expression producing the new value (same fields/meta/raw env as filters)

	// Kind == "rate-limit"
	MaxPerSecond float64 `json:"maxPerSecond,omitempty"`

	// Kind == "dedup"
	KeyExpr string `json:"keyExpr,omitempty"` // CEL expression producing the dedup key (must evaluate to a string; non-string results are formatted via fmt.Sprint)
	Window  string `json:"window,omitempty"`  // Go duration string (e.g. "30s"); empty defaults to "30s"

	// Kind == "schema"
	RequiredFields []string `json:"requiredFields,omitempty"`
}

// SinkCfg describes the destination for passing messages.
type SinkCfg struct {
	Type    string `json:"type"`              // "nats" | "http" | "stdout"
	URL     string `json:"url,omitempty"`     // NATS URL or HTTP base URL
	Subject string `json:"subject,omitempty"` // NATS subject or HTTP path suffix

	// HTTP-specific (Type == "http")
	Method      string            `json:"method,omitempty"`      // GET|POST|PUT|PATCH|DELETE; default POST
	Headers     map[string]string `json:"headers,omitempty"`     // extra static headers, sent on every request
	ContentType string            `json:"contentType,omitempty"` // default "application/json"
	BodyMode    string            `json:"bodyMode,omitempty"`    // "wrapped" (default) | "raw" — see sink/http.go
	Auth        *HTTPAuth         `json:"auth,omitempty"`        // nil = no auth (today's behavior)

	// NATS-specific (Type == "nats")
	NATSAuth *NATSAuth `json:"natsAuth,omitempty"` // nil = no auth (today's behavior)

	// When is an optional CEL expression (same fields/meta/raw env as filters)
	// evaluated per-message to decide whether THIS sink should receive it.
	// Empty = always send. Non-empty = only send when the expression is true.
	When string `json:"when,omitempty"`
}

// HTTPAuth configures authentication for the HTTP sink. Type selects which
// fields below apply.
type HTTPAuth struct {
	Type       string `json:"type"`                 // "bearer" | "basic" | "apiKey"
	Token      string `json:"token,omitempty"`      // bearer: sent as "Authorization: Bearer <token>"
	Username   string `json:"username,omitempty"`   // basic
	Password   string `json:"password,omitempty"`   // basic
	HeaderName string `json:"headerName,omitempty"` // apiKey: header name; default "X-API-Key" if empty
	APIKey     string `json:"apiKey,omitempty"`     // apiKey: header value
}

// NATSAuth configures authentication/TLS for the NATS sink. All credential
// material here is INLINE CONTENT (raw strings), never a filesystem path.
// This is deliberate: a ListenerDefinition travels as a single
// self-contained JSON blob (via the LISTENER_SPEC env var) to whichever
// process ultimately runs it — including, in Docker/Kubernetes deployment
// modes, a completely different container/Pod filesystem than the
// controller's. A file path would silently break in those modes.
//
// Precedence when multiple credential fields are set (most-specific wins,
// enforced in newNATS): Creds > NKeySeed > Token > Username/Password.
type NATSAuth struct {
	Username           string `json:"username,omitempty"`
	Password           string `json:"password,omitempty"`
	Token              string `json:"token,omitempty"`
	NKeySeed           string `json:"nkeySeed,omitempty"` // raw NKey seed string (starts with "S...")
	Creds              string `json:"creds,omitempty"`    // full inline contents of a .creds file (contains both a JWT block and a seed block)
	TLSCert            string `json:"tlsCert,omitempty"`  // inline client certificate, PEM
	TLSKey             string `json:"tlsKey,omitempty"`   // inline client private key, PEM
	TLSCA              string `json:"tlsCa,omitempty"`    // inline CA certificate, PEM
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
}
