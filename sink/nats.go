package sink

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/cnak-us/gogo/message"
	"github.com/cnak-us/gogo/spec"
)

type natsSink struct {
	nc      *nats.Conn
	subject string
}

func newNATS(cfg spec.SinkCfg, listenerName string) (*natsSink, error) {
	url := cfg.URL
	if url == "" {
		url = nats.DefaultURL
	}
	nc, err := nats.Connect(url,
		nats.Name("cnak-ingress:"+listenerName),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect %s: %w", url, err)
	}
	subj := cfg.Subject
	if subj == "" {
		subj = "ingress." + listenerName
	}
	return &natsSink{nc: nc, subject: subj}, nil
}

// Send publishes the message as JSON containing both fields and meta.
func (s *natsSink) Send(msg *message.Message) error {
	payload := map[string]interface{}{
		"fields": msg.Fields,
		"meta":   msg.Meta,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("nats sink: marshal: %w", err)
	}
	return s.nc.Publish(s.subject, data)
}

func (s *natsSink) Close() error {
	s.nc.Drain()
	return nil
}
