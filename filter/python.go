package filter

import (
	"context"
	"log/slog"
	"sync"

	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// Observed is implemented by stages that capture per-run diagnostics; the
// simulator copies them into the stage trace.
type Observed interface {
	Observation() (stdout string, durationUs int64)
}

type pythonStage struct {
	ref        *spec.PythonRef
	mu         sync.Mutex
	stdout     string
	durationUs int64
}

func (p *pythonStage) Observation() (string, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stdout, p.durationUs
}

func (p *pythonStage) Process(msg *message.Message) (bool, error) {
	res, err := functionrpc.Invoke(context.Background(), p.ref, "transformer", msg)
	if res != nil {
		p.mu.Lock()
		p.stdout, p.durationUs = res.Stdout, res.DurationUs
		p.mu.Unlock()
		if res.Stdout != "" && slog.Default().Enabled(context.Background(), slog.LevelDebug) {
			out := res.Stdout
			if len(out) > 2048 {
				out = out[:2048]
			}
			slog.Debug("Python stage output", "function", p.ref.Function, "stdout", out)
		}
	}
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
