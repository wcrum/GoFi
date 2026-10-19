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

	// Payload, when set, is the exact bytes a sink must send: the result of
	// that sink's configured Encoding. Sinks send it verbatim instead of
	// their default serialization.
	Payload []byte

	// DecodedBytes totals the output of every decoder step run for this
	// message: the flow's decode step and each parse stage's decoders. The
	// codec package adds to it and rejects a step that would push it past
	// the per-message budget, so a small frame cannot expand without bound
	// across stages.
	DecodedBytes int

	// rawString caches RawString for the Raw slice it was taken from, so
	// every CEL program evaluated against one message shares a single copy.
	rawString    string
	rawStringFor []byte
}

// RawString is Raw as a string, converted at most once per distinct Raw
// slice: stages that replace Raw get a fresh conversion, repeated
// evaluations (several filters, sink conditions) reuse the cached one. A
// message is processed by one goroutine at a time, like Fields and Meta.
func (m *Message) RawString() string {
	if len(m.Raw) == 0 {
		return ""
	}
	if len(m.rawStringFor) == len(m.Raw) && &m.rawStringFor[0] == &m.Raw[0] {
		return m.rawString
	}
	m.rawString = string(m.Raw)
	m.rawStringFor = m.Raw
	return m.rawString
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
