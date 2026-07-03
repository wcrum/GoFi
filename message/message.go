package message

import "time"

// Message is the envelope that flows through each pipeline stage.
type Message struct {
	// Raw holds the unmodified frame bytes as received from the source.
	Raw []byte

	// Fields holds the structured key-value pairs produced by the parser.
	// Values are string, float64, bool, or []interface{} — JSON-compatible types.
	Fields map[string]interface{}

	// Meta holds pipeline metadata injected by the listener.
	Meta map[string]string
}

func New(raw []byte, listenerName, remoteAddr string) *Message {
	return &Message{
		Raw:    raw,
		Fields: make(map[string]interface{}),
		Meta: map[string]string{
			"name":   listenerName,
			"source": remoteAddr,
			"ts":     time.Now().UTC().Format(time.RFC3339Nano),
		},
	}
}
