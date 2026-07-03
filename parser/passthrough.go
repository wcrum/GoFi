package parser

import "github.com/cnak-us/gogo/message"

type passthroughParser struct{}

func (p *passthroughParser) Parse(msg *message.Message) error {
	msg.Fields["raw"] = string(msg.Raw)
	return nil
}
