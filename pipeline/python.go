package pipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wcrum/gofi/codec"
	"github.com/wcrum/gofi/framing"
	"github.com/wcrum/gofi/functionrpc"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// Python steps for the codec and framing packages run through the worker
// RPC client; installing them here keeps those packages free of it.
func init() {
	codec.SetPythonRunner(func(ctx context.Context, ref *spec.PythonRef, kind string, data []byte, msg *message.Message) ([]byte, error) {
		in := *msg
		in.Raw = data
		res, err := functionrpc.Invoke(ctx, ref, kind, &in)
		if err != nil {
			return nil, err
		}
		if res.Raw == nil {
			return nil, fmt.Errorf("Python %s %s.%s must return bytes", kind, ref.Library, ref.Function)
		}
		return res.Raw, nil
	})
	framing.SetPythonRunner(func(ctx context.Context, ref *spec.PythonRef, buffer []byte, final bool) ([][]byte, []byte, error) {
		msg := &message.Message{Raw: buffer, Fields: map[string]any{}, Meta: map[string]string{"final": fmt.Sprint(final)}}
		res, err := functionrpc.Invoke(ctx, ref, "framer", msg)
		if err != nil {
			return nil, nil, err
		}
		return framing.DecodePythonFrames(res.Fields.AsMap())
	})
}

// Encode returns the message a sink sends: msg itself when enc is nil, or a
// copy whose Payload is the serialized, encoded bytes.
func Encode(ctx context.Context, enc *spec.Encoding, msg *message.Message) (*message.Message, error) {
	if enc == nil {
		return msg, nil
	}
	var data []byte
	var err error
	switch enc.Format {
	case "raw":
		data = msg.Raw
	case "fields":
		data, err = json.Marshal(msg.Fields)
	default:
		data, err = json.Marshal(map[string]any{"id": msg.ID, "fields": msg.Fields, "meta": msg.Meta})
	}
	if err != nil {
		return nil, fmt.Errorf("encoding: %w", err)
	}
	if data, err = codec.Encode(ctx, enc.Encoders, data, 0, msg); err != nil {
		return nil, fmt.Errorf("encoding: %w", err)
	}
	out := *msg
	out.Payload = data
	return &out, nil
}

func deadLetterEncoding(def spec.FlowDefinition) *spec.Encoding {
	if def.DeadLetterSink == nil {
		return nil
	}
	return def.DeadLetterSink.Encoding
}
