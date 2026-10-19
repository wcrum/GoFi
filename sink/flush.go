package sink

import (
	"context"
	"time"
)

// Flusher is a Sink that buffers sends. Flush returns once everything sent so
// far has left the process, or ctx ends.
type Flusher interface {
	Flush(ctx context.Context) error
}

// Flush flushes s when it buffers; other sinks send synchronously, so there is
// nothing to wait for.
func Flush(ctx context.Context, s Sink) error {
	if f, ok := s.(Flusher); ok {
		return f.Flush(ctx)
	}
	return nil
}

// Flush waits for buffered publishes to reach the NATS server.
func (s *natsSink) Flush(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	return s.nc.FlushWithContext(ctx)
}
