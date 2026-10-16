package pipeline

import (
	"context"
	"encoding/json"
	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// Listener hooks run after framing and before parsing. A mapping result is
// serialized as JSON for the selected parser; bytes replace the input frame.
func applySourceHook(ctx context.Context, ref *spec.PythonRef, msg *message.Message) (bool, error) {
	if ref == nil {
		return false, nil
	}
	res, err := functionrpc.Invoke(ctx, ref, "listener", msg)
	if err != nil {
		return false, err
	}
	if res.Drop {
		return true, nil
	}
	if res.Raw != nil {
		msg.Raw = res.Raw
	} else if res.Fields != nil {
		msg.Raw, err = json.Marshal(res.Fields.AsMap())
	}
	return false, err
}
