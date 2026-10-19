package source

import "github.com/wcrum/gofi/spec"

// AcksAfterEmit reports whether src acknowledges each message to its broker
// only after emit returns (Kafka, AMQP, JetStream, Redis Streams). Such
// messages must be processed on the consumer goroutine, in order, so the
// acknowledgement means the pipeline ran; the listener never spreads them
// over a worker pool. Every other source delivers at most once anyway.
func AcksAfterEmit(src spec.FlowSource) bool {
	s := src.Subscribe
	if s == nil {
		return false
	}
	switch {
	case s.Kafka != nil, s.AMQP != nil, s.JetStream != nil:
		return true
	case s.Redis != nil:
		return s.Redis.Stream != ""
	}
	return false
}
