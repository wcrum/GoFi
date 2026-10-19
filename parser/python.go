package parser

import (
	"context"
	"fmt"
	"sync"

	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

type pythonParser struct {
	ref        *spec.PythonRef
	mu         sync.Mutex
	stdout     string
	durationUs int64
}

// Observation reports the last call's captured output and duration (used by
// simulation traces of parse stages).
func (p *pythonParser) Observation() (string, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stdout, p.durationUs
}

func (p *pythonParser) Parse(msg *message.Message) error {
	res, err := functionrpc.Invoke(context.Background(), p.ref, "parser", msg)
	if res != nil {
		p.mu.Lock()
		p.stdout, p.durationUs = res.Stdout, res.DurationUs
		p.mu.Unlock()
	}
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
