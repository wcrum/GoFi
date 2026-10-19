package retry

import (
	"context"
	"log/slog"
	"time"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/sink"
)

// Sink wraps a sink.Sink so every Send is retried under Policy until it
// succeeds or ctx (the flow's lifetime) ends. Permanent errors (for example
// a 4xx policy denial marked with Permanent by the caller) are returned at
// once. It is the simplest way to make an HTTP/TCP/NATS sink survive a
// network outage without any queueing; the data-plane sink queue can use
// retry.Backoff directly for finer control.
type Sink struct {
	Next   sink.Sink
	Policy Policy
	ctx    context.Context
	// Flow and Kind label log lines; both optional.
	Flow, Kind string
	// Permanent, when set, decides which delivery errors are not retried.
	Permanent func(error) bool
}

// WrapSink returns s retried under p for as long as ctx lives.
func WrapSink(ctx context.Context, s sink.Sink, p Policy) *Sink {
	return &Sink{Next: s, Policy: p, ctx: ctx}
}

// Send delivers msg, retrying with capped backoff on failure.
func (s *Sink) Send(msg *message.Message) error {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return Do(ctx, s.Policy, func(context.Context) error {
		err := s.Next.Send(msg)
		if err != nil && s.Permanent != nil && s.Permanent(err) {
			return Permanent(err)
		}
		return err
	}, func(attempt int, err error, next time.Duration) {
		slog.Warn("sink delivery failed; retrying", "flow", s.Flow, "sink", s.Kind, "attempt", attempt, "error", err, "retryIn", next.Round(time.Millisecond))
	})
}

// Close closes the wrapped sink.
func (s *Sink) Close() error { return s.Next.Close() }
