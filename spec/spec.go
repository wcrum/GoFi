package spec

// FlowDefinition describes GoFi's top-level object, a flow:
// a source → framer → decode → parser → ordered stage chain → sink(s).
// See docs/flows.md.
type FlowDefinition struct {
	Name    string     `json:"name"`
	Source  FlowSource `json:"source"`
	Framing Framing    `json:"framing"`
	// Decode runs ordered decoding steps on each frame before the parser.
	// Older definitions carried these as parser.decoders and
	// parser.maxDecodedBytes; Normalize moves them here.
	Decode         *DecodeCfg  `json:"decode,omitempty"`
	Parser         ParserCfg   `json:"parser"`
	Stages         []StageSpec `json:"stages,omitempty"`
	Sinks          []SinkCfg   `json:"sinks,omitempty"`
	DeadLetterSink *SinkCfg    `json:"deadLetterSink,omitempty"` // optional; receives messages dropped by the stage chain
	TestCases      []TestCase  `json:"testCases,omitempty"`
	// Python holds flow-wide Python settings: the default library of every
	// Python step that names only a function.
	Python *FlowPython `json:"python,omitempty"`
	// State is the declared run state: "running" (default when empty) or
	// "stopped". A stopped flow keeps its definition but has no runtime.
	State string `json:"state,omitempty"`
}

// TestCase is a saved mock-mode sample input for exercising this flow's
// stage chain without real traffic. Purely descriptive: the runtime
// (listener/pipeline packages) never reads this field — it exists solely for
// the editor's mock-mode panel and pipeline.Simulate.
type TestCase struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Raw  string `json:"raw"` // raw frame text fed through Simulate
}

// ListenSource is a flow source where senders push to a GoFi TCP, UDP, or
// QUIC port (the former Listener.source).
type ListenSource struct {
	Port    int              `json:"port"`
	Network string           `json:"network"`           // "tcp" | "udp" | "quic" (QUIC runs over UDP on Port)
	Service *ServiceExposure `json:"service,omitempty"` // Kubernetes exposure; nil leaves the port private
	// TLS is the QUIC listen source's TLS material (quic.go). nil means an
	// ephemeral self-signed certificate generated at startup. Only valid
	// with network quic.
	TLS *TLSConfig `json:"tls,omitempty"`
}

// ServiceExposure publishes a listener through a stable Kubernetes Service.
// Destinations are addressed by DNS/URL; pod selectors are internal to GoFi.
type ServiceExposure struct {
	Type     string `json:"type"`               // "ClusterIP" | "NodePort" | "LoadBalancer" | "Gateway"
	Port     int    `json:"port,omitempty"`     // defaults to source.port; the portal leaves it unset
	NodePort int    `json:"nodePort,omitempty"` // NodePort/LoadBalancer; 0 requests allocation from the cluster
	// ExternalTrafficPolicy is "Cluster" (default) or "Local" (NodePort and
	// LoadBalancer). Local keeps the sender's source IP.
	ExternalTrafficPolicy string               `json:"externalTrafficPolicy,omitempty"`
	Annotations           map[string]string    `json:"annotations,omitempty"`
	LoadBalancer          *LoadBalancerOptions `json:"loadBalancer,omitempty"`
	Gateway               *GatewayExposure     `json:"gateway,omitempty"`
	// AllowFrom limits in-cluster senders (ClusterIP) to these workloads,
	// typically chosen from their UDS Packages. Empty allows every namespace.
	AllowFrom []PeerRef `json:"allowFrom,omitempty"`
}

// PeerRef names workloads in another namespace: a UDS Package (by name, for
// display and verification) and the Pod labels the allow should match.
type PeerRef struct {
	Namespace string            `json:"namespace"`
	Package   string            `json:"package,omitempty"`
	Selector  map[string]string `json:"selector,omitempty"`
}

// LoadBalancerOptions chooses which load balancer serves a LoadBalancer Service.
type LoadBalancerOptions struct {
	Class       string `json:"class,omitempty"`       // spec.loadBalancerClass
	AddressPool string `json:"addressPool,omitempty"` // MetalLB IPAddressPool
	IP          string `json:"ip,omitempty"`          // requested address
}

// GatewayExposure attaches the flow to a Gateway API Gateway with a TCPRoute
// or UDPRoute. With an empty Listener, GoFi adds a gofi-<flow> listener on
// Port (default: the listen port) when the chart allows it.
type GatewayExposure struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Listener  string `json:"listener,omitempty"`
	Port      int    `json:"port,omitempty"`
}

// Framing controls how the raw byte stream is split into discrete frames.
type Framing struct {
	// Mode is one of: "line", "delimited", "length-prefix", "raw".
	//   line          — split on \n
	//   delimited     — accumulate until Delimiter byte sequence is found
	//   length-prefix — read a big-endian uint16 or uint32 length header, then that many bytes
	//   raw           — entire connection is one frame (closed on disconnect)
	//   python        — a @gofi.framer function splits the byte stream
	Mode      string     `json:"mode"`
	Delimiter string     `json:"delimiter,omitempty"` // used by "delimited"
	PrefixLen int        `json:"prefixLen,omitempty"` // 2 or 4; used by "length-prefix"
	MaxBytes  int        `json:"maxBytes,omitempty"`  // safety cap; 0 = 1 MiB
	Python    *PythonRef `json:"python,omitempty"`    // used by "python"
}

// ParserCfg selects how a frame's bytes are decoded into Message.Fields.
type ParserCfg struct {
	Python *PythonRef `json:"python,omitempty"`
	// Format is one of: "json", "xml", "none", "text", "python".
	//   json   — top-level keys become Fields
	//   xml    — top-level attributes and first-level child text nodes become Fields
	//   none   — Fields["raw"] = decoded frame as UTF-8 string
	//   text   — same as none (the parse stage's name for it)
	//   python — a @gofi.parser function returns the fields
	Format string `json:"format"`

	// LegacyDecoders and LegacyMaxDecodedBytes are the pre-decode-step
	// location of FlowDefinition.Decode. They are read for compatibility
	// only: Normalize moves them into Decode and clears them, so nothing is
	// ever written in this shape. Runtime code never reads them.
	//
	// Deprecated: use FlowDefinition.Decode.
	LegacyDecoders []CodecStep `json:"decoders,omitempty"`
	// Deprecated: use FlowDefinition.Decode.MaxDecodedBytes.
	LegacyMaxDecodedBytes int `json:"maxDecodedBytes,omitempty"`
	// ErrorPolicy is "continue" (default) or "drop". Drop sends malformed
	// frames to the dead-letter sink when configured, before running stages.
	ErrorPolicy string `json:"errorPolicy,omitempty"`
}

// DecodeCfg is the flow's decode step: byte transformations applied to each
// frame, in order, before the parser. Raw frame bytes remain unchanged.
type DecodeCfg struct {
	// Decoders are built-in codecs (base64, gzip, …; see BuiltinCodecs) or
	// Python @gofi.decoder functions.
	Decoders []CodecStep `json:"decoders,omitempty"`
	// MaxDecodedBytes caps the output of each decoding step; 0 = 1 MiB.
	MaxDecodedBytes int `json:"maxDecodedBytes,omitempty"`
}

// FlowPython is flow-wide Python configuration.
type FlowPython struct {
	// Library is the default library for every PythonRef in the flow that
	// names a function without a library (inline source is unaffected).
	Library string `json:"library,omitempty"`
}

// ParseStage configures a stage of kind "parse": it parses a value already
// on the message (the raw frame or a field) and adds the result to fields.
// A flow can parse any number of times, e.g. a JSON document inside a syslog
// line, then a base64-gzip field inside that document.
type ParseStage struct {
	// From is a CEL expression (fields/meta/raw environment) evaluated per
	// message that must yield a string or bytes. Empty or "raw" uses the
	// frame's raw bytes.
	From string `json:"from,omitempty"`
	// Decoders run in order on the input before parsing (same steps as
	// decode.decoders); each step's output is capped at 1 MiB.
	Decoders []CodecStep `json:"decoders,omitempty"`
	// Format is "json", "xml", "text", or "python".
	Format string `json:"format"`
	// Python is the @gofi.parser function for format "python". The library
	// may come from the flow's python.library.
	Python *PythonRef `json:"python,omitempty"`
	// Into is where the result goes: empty merges it into the top-level
	// fields; otherwise it is stored at fields[into] (a nested map; for
	// format "text", the decoded string).
	Into string `json:"into,omitempty"`
	// Mode is "merge" (default) or "replace": the parse result becomes the
	// message's fields. Replace requires an empty Into.
	Mode string `json:"mode,omitempty"`
	// OnError is "continue" (default: the message passes unchanged and the
	// error is reported) or "drop".
	OnError string `json:"onError,omitempty"`
}

// StageSpec configures one processing block in the ordered chain between
// ingress and the sink(s). Stages run in array order. Kind selects behavior:
//
//	"filter"     — evaluate Expression (Type-specific, "cel" today); false drops the message
//	"transform"  — mutate the message in place; never drops (aside from an eval error, which fails open)
//	"rate-limit" — drop when the listener-wide rate exceeds MaxPerSecond (approximate per-second window, shared across all connections of this listener)
//	"dedup"      — drop repeats of KeyExpr's value seen within Window (shared across all connections of this listener)
//	"schema"     — drop messages missing any entry in RequiredFields
//	"parse"      — parse the raw frame or a field again (see ParseStage)
//	"python"     — run a @gofi.transformer function
//
// Messages that are dropped by any stage short-circuit the remaining chain.
type StageSpec struct {
	Python *PythonRef `json:"python,omitempty"`
	ID     string     `json:"id"`   // client-generated, stable, unique in chain
	Kind   string     `json:"kind"` // "filter" | "transform" | "rate-limit" | "dedup" | "schema" | "parse" | "python"

	// Kind == "filter"
	Type       string `json:"type,omitempty"`       // "cel" | "regex"; empty/unknown = error
	Expression string `json:"expression,omitempty"` // CEL expression or RE2 regular expression

	// Kind == "transform"
	TransformOp string `json:"transformOp,omitempty"` // "set" | "rename" | "remove"
	Field       string `json:"field,omitempty"`       // transform target; regex input field (empty uses original raw frame)
	NewField    string `json:"newField,omitempty"`    // "rename": destination field name
	ValueExpr   string `json:"valueExpr,omitempty"`   // "set": CEL expression producing the new value (same fields/meta/raw env as filters)

	// Kind == "rate-limit"
	MaxPerSecond float64 `json:"maxPerSecond,omitempty"`

	// Kind == "dedup"
	KeyExpr string `json:"keyExpr,omitempty"` // CEL expression producing the dedup key (must evaluate to a string; non-string results are formatted via fmt.Sprint)
	Window  string `json:"window,omitempty"`  // Go duration string (e.g. "30s"); empty defaults to "30s"

	// Kind == "schema"
	RequiredFields []string `json:"requiredFields,omitempty"`

	// Kind == "parse"
	Parse *ParseStage `json:"parse,omitempty"`

	// ExpressionRef links this kind's expression (Expression, ValueExpr, or
	// KeyExpr) to a CEL library entry; a literal expression takes precedence.
	ExpressionRef *ExpressionRef `json:"expressionRef,omitempty"`
	// OnError selects CEL evaluation-error handling: "pass" (default) or "drop".
	OnError string `json:"onError,omitempty"`
}

// ExpressionRef names a CEL library entry. Generation 0 follows the latest
// generation; a positive generation pins that revision.
type ExpressionRef struct {
	Name       string `json:"name"`
	Generation int64  `json:"generation,omitempty"`
}

// SinkCfg describes the destination for passing messages.
type SinkCfg struct {
	Labels     map[string]string `json:"labels,omitempty"` // Loki static stream labels.
	Kubernetes *KubernetesTarget `json:"kubernetes,omitempty"`
	Python     *PythonRef        `json:"python,omitempty"`
	Type       string            `json:"type"`                // "nats" | "http" | "tcp" | "udp" | "quic" | "aster" | "stdout"
	URL        string            `json:"url,omitempty"`       // NATS URL, HTTP base URL, or tcp|udp|quic://host:port
	Subject    string            `json:"subject,omitempty"`   // NATS subject or HTTP path suffix
	Delimiter  string            `json:"delimiter,omitempty"` // TCP/UDP/QUIC: suffix appended to each payload; empty preserves bytes
	// TLS configures the QUIC sink's client side (quic.go): server
	// verification (system roots by default), optional client certificate.
	TLS *TLSConfig `json:"tls,omitempty"`

	// HTTP-specific (Type == "http")
	Method      string            `json:"method,omitempty"`      // GET|POST|PUT|PATCH|DELETE; default POST
	Headers     map[string]string `json:"headers,omitempty"`     // extra static headers, sent on every request
	ContentType string            `json:"contentType,omitempty"` // default "application/json"
	BodyMode    string            `json:"bodyMode,omitempty"`    // "wrapped" (default) | "raw" — see sink/http.go
	Auth        *HTTPAuth         `json:"auth,omitempty"`        // nil = no auth (today's behavior)

	// NATS-specific (Type == "nats")
	NATSAuth *NATSAuth `json:"natsAuth,omitempty"` // nil = no auth (today's behavior)

	// Aster-specific (Type == "aster"). URL must be literal loopback HTTP.
	Aster *AsterCfg `json:"aster,omitempty"`

	// When is an optional CEL expression (same fields/meta/raw env as filters)
	// evaluated per-message to decide whether THIS sink should receive it.
	// Empty = always send. Non-empty = only send when the expression is true.
	When string `json:"when,omitempty"`
	// WhenRef links the condition to a CEL library entry; When takes precedence.
	WhenRef *ExpressionRef `json:"whenRef,omitempty"`
	// Encoding serializes and encodes the message before this sink sends it.
	Encoding *Encoding `json:"encoding,omitempty"`
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
// This is deliberate: a FlowDefinition travels as a single
// self-contained JSON blob (via the FLOW_SPEC env var) to whichever
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
