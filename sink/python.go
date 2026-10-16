package sink

import (
	"context"
	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

type pythonSink struct{ ref *spec.PythonRef }

func (p *pythonSink) Send(msg *message.Message) error {
	_, err := functionrpc.Invoke(context.Background(), p.ref, "sink", msg)
	return err
}
func (p *pythonSink) Close() error { return nil }
