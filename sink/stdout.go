package sink

import (
	"encoding/json"
	"fmt"

	"github.com/cnak-us/gogo/message"
)

type stdoutSink struct{}

func (s *stdoutSink) Send(msg *message.Message) error {
	out := map[string]interface{}{
		"fields": msg.Fields,
		"meta":   msg.Meta,
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func (s *stdoutSink) Close() error { return nil }
