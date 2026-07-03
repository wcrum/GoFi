package spec

// ListenerDefinition describes a full ingest pipeline:
// a network source → framer → parser → optional CEL filter → sink.
type ListenerDefinition struct {
	Name    string    `json:"name"`
	Source  Source    `json:"source"`
	Framing Framing   `json:"framing"`
	Parser  ParserCfg `json:"parser"`
	Filter  *Filter   `json:"filter,omitempty"`
	Sink    SinkCfg   `json:"sink"`
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

// Filter holds a CEL expression evaluated against each parsed message.
// Messages that evaluate to false are dropped.
type Filter struct {
	Expression string `json:"expression"`
}

// SinkCfg describes the destination for passing messages.
type SinkCfg struct {
	Type    string `json:"type"`             // "nats" | "http" | "stdout"
	URL     string `json:"url,omitempty"`    // NATS URL or HTTP base URL
	Subject string `json:"subject,omitempty"` // NATS subject or HTTP path suffix
}
