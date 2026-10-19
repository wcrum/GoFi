package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/parser"
	"github.com/wcrum/gofi/spec"
)

// ParseSample prepares raw the way def's listener would before any stage
// runs: the optional listener hook, then decoders and the parser. Parse
// errors are recorded in meta["parseError"] (as on the live path) rather
// than returned, so CEL expressions can be tested against malformed input.
func ParseSample(ctx context.Context, def spec.FlowDefinition, raw []byte) (*message.Message, error) {
	def, err := def.Prepared()
	if err != nil {
		return nil, err
	}
	pr, err := parser.ForFlow(def)
	if err != nil {
		return nil, fmt.Errorf("parser: %w", err)
	}
	msg := message.New(raw, def.Name, "evaluate")
	if perr := pr.Parse(msg); perr != nil && !errors.Is(perr, parser.ErrRejected) {
		msg.Meta["parseError"] = perr.Error()
	}
	return msg, nil
}
