package sink

import (
	"encoding/json"
	"fmt"

	"github.com/wcrum/gofi/message"
)

type stdoutSink struct{}

func (s *stdoutSink) Send(msg *message.Message) error {
	if msg.Payload != nil {
		fmt.Println(string(msg.Payload))
		return nil
	}
	out := map[string]interface{}{
		"fields": msg.Fields,
		"meta":   msg.Meta,
	}
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func (s *stdoutSink) Close() error { return nil }
