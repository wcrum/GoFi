// Package sink defines where processed messages are delivered.
package sink

import (
	"fmt"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// Sink receives a processed message.
type Sink interface {
	Send(msg *message.Message) error
	Close() error
}

// New constructs a Sink from config. For NATS sinks, listenerName is used
// as the NATS connection name for observability.
func New(cfg spec.SinkCfg, listenerName string) (Sink, error) {
	switch cfg.Type {
	case "nats":
		return newNATS(cfg, listenerName)
	case "http":
		return newHTTP(cfg)
	case "stdout", "":
		return &stdoutSink{}, nil
	default:
		return nil, fmt.Errorf("sink: unknown type %q", cfg.Type)
	}
}
