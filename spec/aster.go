package spec

// AsterCfg publishes Events through the authenticated loopback-only Aster
// application agent in the listener's dedicated network namespace.
type AsterCfg struct {
	Topic           string `json:"topic"`
	Scope           string `json:"scope"`
	Priority        string `json:"priority,omitempty"`        // routine (default), priority, immediate, flash
	LogicalKey      string `json:"logicalKey,omitempty"`      // UTF-8 text, encoded as protobuf bytes
	LogicalKeyField string `json:"logicalKeyField,omitempty"` // optional parsed string field instead of LogicalKey
	Token           string `json:"token,omitempty"`
	TokenFile       string `json:"tokenFile,omitempty"`      // mounted local file; GOFI_ASTER_TOKEN_FILE is the fallback
	TimeoutSeconds  int    `json:"timeoutSeconds,omitempty"` // default 10, maximum 30
}
