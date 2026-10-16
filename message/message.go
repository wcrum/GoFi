package message

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Message is the envelope that flows through each pipeline stage.
type Message struct {
	// ID identifies this ingest event across attempts to send the same message.
	// It is in-memory identity, not a durable publication journal.
	ID string

	// Raw starts as the incoming frame. Built-in decoding preserves it; an
	// explicitly configured Python hook or transform may replace it.
	Raw []byte

	// Fields holds the structured key-value pairs produced by the parser.
	// Values are string, float64, bool, or []interface{} — JSON-compatible types.
	Fields map[string]interface{}

	// Meta holds pipeline metadata injected by the listener.
	Meta map[string]string
}

func New(raw []byte, listenerName, remoteAddr string) *Message {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return &Message{
		ID:     hex.EncodeToString(id[:]),
		Raw:    raw,
		Fields: make(map[string]interface{}),
		Meta: map[string]string{
			"name":   listenerName,
			"source": remoteAddr,
			"ts":     time.Now().UTC().Format(time.RFC3339Nano),
		},
	}
}
