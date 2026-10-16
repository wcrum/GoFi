package spec

import (
	"encoding/json"
	"fmt"
)

// BuiltinCodecs are the byte transformations GoFi implements natively. Each
// works in both directions: decoding before parsing, encoding before a sink.
var BuiltinCodecs = []string{"base64", "base64url", "hex", "gzip", "zlib", "url"}

// CodecStep is one ordered decoding or encoding step: a built-in codec, or a
// Python function (Type "python": @gofi.decoder or @gofi.encoder).
type CodecStep struct {
	Type   string     `json:"type"`
	Python *PythonRef `json:"python,omitempty"`
}

// UnmarshalJSON also accepts a bare built-in name ("base64"), the natural
// shorthand when writing Flow resources by hand.
func (c *CodecStep) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		*c = CodecStep{Type: name}
		return nil
	}
	type plain CodecStep
	return json.Unmarshal(data, (*plain)(c))
}

// Validate checks one step; `role` is "decoder" or "encoder" for messages.
func (c CodecStep) Validate(role string) error {
	if c.Type == "python" {
		if err := c.Python.Validate(); err != nil {
			return fmt.Errorf("%s: %w", role, err)
		}
		return nil
	}
	if c.Python != nil {
		return fmt.Errorf("%s %q: python is only valid with type python", role, c.Type)
	}
	for _, name := range BuiltinCodecs {
		if c.Type == name {
			return nil
		}
	}
	return fmt.Errorf("unknown %s %q (built-ins: base64, base64url, hex, gzip, zlib, url; or python)", role, c.Type)
}

// Encoding is how a message becomes the bytes a sink sends: a format, then
// encoders in order. A nil Encoding keeps the sink's default payload.
type Encoding struct {
	// Format is "wrapped" (JSON of id, fields, and meta), "fields" (JSON of
	// fields), or "raw" (the original frame bytes). Empty means "wrapped".
	Format   string      `json:"format,omitempty"`
	Encoders []CodecStep `json:"encoders,omitempty"`
}

// Validate checks the format and every encoder.
func (e *Encoding) Validate() error {
	if e == nil {
		return nil
	}
	switch e.Format {
	case "", "wrapped", "fields", "raw":
	default:
		return fmt.Errorf("encoding.format must be wrapped, fields, or raw")
	}
	for _, step := range e.Encoders {
		if err := step.Validate("encoder"); err != nil {
			return err
		}
	}
	return nil
}
