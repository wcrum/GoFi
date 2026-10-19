package sink

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spool"
)

// Durable overflow. With config.Limits.SinkSpoolDir set (GOFI_SINK_SPOOL_DIR),
// a queued sink whose memory queue is full appends messages to an on-disk
// spool (one directory per flow and sink) instead of applying the drop
// policy, and messages that arrive while the circuit breaker is open go
// there too. The worker drains the spool whenever the memory queue is idle
// and the breaker is closed, oldest first, in batches of BatchMax. The drop
// policy applies only once the spool itself is full (GOFI_SINK_SPOOL_MAX_BYTES;
// drop-oldest deletes the oldest segment, anything else refuses).

// spoolSyncInterval batches fsyncs of appended records.
const spoolSyncInterval = time.Second

// spoolPollInterval is how often an idle worker looks for spooled records.
const spoolPollInterval = 250 * time.Millisecond

// spooledMessage is the on-disk form of a message: what a sink needs to
// send it later (byte slices travel base64-encoded in JSON).
type spooledMessage struct {
	ID      string            `json:"id"`
	Raw     []byte            `json:"raw,omitempty"`
	Fields  map[string]any    `json:"fields,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
	Payload []byte            `json:"payload,omitempty"`
}

func encodeSpooled(m *message.Message) ([]byte, error) {
	return json.Marshal(spooledMessage{ID: m.ID, Raw: m.Raw, Fields: m.Fields, Meta: m.Meta, Payload: m.Payload})
}

func decodeSpooled(data []byte) (*message.Message, error) {
	var s spooledMessage
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Fields == nil {
		s.Fields = map[string]any{}
	}
	if s.Meta == nil {
		s.Meta = map[string]string{}
	}
	return &message.Message{ID: s.ID, Raw: s.Raw, Fields: s.Fields, Meta: s.Meta, Payload: s.Payload}, nil
}

// openSpool opens the sink's spool under dir, or returns nil when dir is empty.
func openSpool(dir, flow, sinkID string, maxBytes int64, dropOldest bool) *spool.Queue {
	if dir == "" {
		return nil
	}
	q, err := spool.Open(filepath.Join(dir, flow, sinkID), spool.Options{MaxBytes: maxBytes, DropOldest: dropOldest, SyncInterval: spoolSyncInterval})
	if err != nil {
		slog.Error("sink spool unavailable; overflow falls back to the drop policy", "flow", flow, "sink", sinkID, "dir", dir, "error", err)
		return nil
	}
	if n := q.Pending(); n > 0 {
		slog.Info("sink spool recovered", "flow", flow, "sink", sinkID, "pending", n)
	}
	return q
}

// spoolMessage appends m to the spool. It reports false when the spool is
// full or unavailable, in which case the caller applies the drop policy.
func (q *QueuedSink) spoolMessage(m *message.Message) bool {
	if q.spool == nil {
		return false
	}
	data, err := encodeSpooled(m)
	if err != nil {
		return false
	}
	if err := q.spool.Push(data); err != nil {
		if time.Since(q.spoolLog) >= dropLogInterval {
			q.spoolLog = time.Now()
			slog.Warn("sink spool refused a message", "flow", q.flow, "sink", q.kind, "error", err)
		}
		return false
	}
	q.metrics.Spooled.Inc()
	q.metrics.SpoolDepth.Set(float64(q.spool.Pending()))
	return true
}

// drainSpool delivers one batch from the spool, oldest first. It returns
// false when there was nothing to drain.
func (q *QueuedSink) drainSpool() bool {
	if q.spool == nil || q.breakerOpen() {
		return false
	}
	records, err := q.spool.Peek(q.cfg.BatchMax, q.cfg.BatchBytes)
	if err != nil || len(records) == 0 {
		return false
	}
	batch := make([]*message.Message, 0, len(records))
	for _, r := range records {
		m, err := decodeSpooled(r.Data)
		if err != nil {
			continue // a corrupt record is acknowledged away with its batch
		}
		batch = append(batch, m)
	}
	// Acknowledge whatever the outcome: after the retries the memory path
	// would also have dropped the batch, and leaving it would replay it
	// forever against a destination that rejects it.
	if q.batcher != nil {
		if len(batch) > 0 {
			q.deliver(batch)
		}
		q.ack(records[len(records)-1])
		return true
	}
	// A sink without batching takes one message per delivery.
	for i, m := range batch {
		q.deliver([]*message.Message{m})
		q.ack(records[i])
		select {
		case <-q.done:
			return false
		default:
		}
	}
	if len(batch) < len(records) {
		q.ack(records[len(records)-1])
	}
	return true
}

func (q *QueuedSink) ack(r spool.Record) {
	if err := q.spool.Ack(r); err != nil {
		slog.Warn("sink spool ack failed", "flow", q.flow, "sink", q.kind, "error", err)
	}
	q.metrics.SpoolDepth.Set(float64(q.spool.Pending()))
}

func (q *QueuedSink) closeSpool() error {
	if q.spool == nil {
		return nil
	}
	if err := q.spool.Close(); err != nil {
		return fmt.Errorf("sink spool close: %w", err)
	}
	return nil
}
