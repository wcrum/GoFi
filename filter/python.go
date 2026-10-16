package filter

import (
	"context"
	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

type pythonStage struct{ ref *spec.PythonRef }

func (p *pythonStage) Process(msg *message.Message) (bool, error) {
	res, err := functionrpc.Invoke(context.Background(), p.ref, "transformer", msg)
	if err != nil {
		return false, err
	} // Custom actions fail closed.
	if res.Fields != nil {
		msg.Fields = res.Fields.AsMap()
	}
	if res.Raw != nil {
		msg.Raw = res.Raw
	}
	return !res.Drop, nil
}
