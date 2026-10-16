package parser

import (
	"context"
	"fmt"
	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

type pythonParser struct{ ref *spec.PythonRef }

func (p *pythonParser) Parse(msg *message.Message) error {
	res, err := functionrpc.Invoke(context.Background(), p.ref, "parser", msg)
	if err != nil {
		return err
	}
	if res.Drop {
		return ErrRejected
	}
	if res.Fields == nil {
		return fmt.Errorf("Python parser must return a mapping")
	}
	msg.Fields = res.Fields.AsMap()
	return nil
}
