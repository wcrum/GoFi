package pipeline

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

func TestDecodedTrafficUsesFieldsAndPreservesWirePayload(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte(`{"app":"payments","status":201}`))
	def := spec.ListenerDefinition{
		Name: "decoded-app", Framing: spec.Framing{Mode: "line"},
		Parser: spec.ParserCfg{Format: "json", Decoders: []string{"base64"}, ErrorPolicy: "drop"},
		Stages: []spec.StageSpec{
			{ID: "app", Kind: "filter", Type: "regex", Field: "app", Expression: `^payments$`},
			{ID: "status", Kind: "filter", Type: "cel", Expression: `fields["status"] >= 200 && fields["status"] < 300`},
		},
		Sinks: []spec.SinkCfg{{Type: "stdout", When: `fields["app"] == "payments" && raw == "` + raw + `"`}},
	}
	chain, err := filter.NewChain(def.Stages)
	if err != nil {
		t.Fatal(err)
	}
	output := &fakeSink{}
	p, err := New(def, strings.NewReader(raw+"\n"), []sink.Sink{output}, nil, chain, NewTap())
	if err != nil {
		t.Fatal(err)
	}
	p.Run(context.Background(), "test")
	msgs := output.received()
	if len(msgs) != 1 || string(msgs[0].Raw) != raw || msgs[0].Fields["app"] != "payments" {
		t.Fatalf("decoded live output = %#v", msgs)
	}
	result, err := Simulate(def, []byte(raw))
	if err != nil || result.Dropped || len(result.Sinks) != 1 || !result.Sinks[0].Sent {
		t.Fatalf("decoded simulation = %#v, %v", result, err)
	}
}

func TestParseErrorPolicyLiveAndSimulation(t *testing.T) {
	for _, policy := range []string{"", "continue", "drop"} {
		t.Run(policy, func(t *testing.T) {
			def := spec.ListenerDefinition{
				Name: "malformed", Framing: spec.Framing{Mode: "line"},
				Parser: spec.ParserCfg{Format: "json", Decoders: []string{"base64"}, ErrorPolicy: policy},
				Stages: []spec.StageSpec{{ID: "must-not-run-on-drop", Kind: "transform", TransformOp: "set", Field: "processed", ValueExpr: "true"}},
				Sinks:  []spec.SinkCfg{{Type: "stdout"}}, DeadLetterSink: &spec.SinkCfg{Type: "stdout"},
			}
			chain, err := filter.NewChain(def.Stages)
			if err != nil {
				t.Fatal(err)
			}
			output, dead := &fakeSink{}, &fakeSink{}
			tap := NewTap()
			sub := tap.Subscribe()
			defer tap.Unsubscribe(sub)
			p, err := New(def, strings.NewReader("invalid!\n"), []sink.Sink{output}, dead, chain, tap)
			if err != nil {
				t.Fatal(err)
			}
			p.Run(context.Background(), "test")
			result, err := Simulate(def, []byte("invalid!"))
			if err != nil || result.ParseError == "" {
				t.Fatalf("parse simulation = %#v, %v", result, err)
			}
			ev := <-sub
			if ev.Msg.Meta["parseError"] == "" {
				t.Fatal("live tap omitted parse error")
			}
			if policy == "drop" {
				if len(output.received()) != 0 || len(dead.received()) != 1 || !ev.Dropped || ev.DroppedBy != "parser" {
					t.Fatalf("parse error did not route to dead letter: %#v", ev)
				}
				if !result.Dropped || result.DroppedBy != "parser" || !result.DeadLetter || len(result.Stages) != 0 {
					t.Fatalf("simulation did not stop at parser: %#v", result)
				}
				if _, ok := dead.received()[0].Fields["processed"]; ok {
					t.Fatal("stage ran after parser rejection")
				}
			} else if len(output.received()) != 1 || len(dead.received()) != 0 || ev.Dropped || result.Dropped {
				t.Fatalf("continue policy changed existing behavior: %#v, %#v", ev, result)
			}
		})
	}
}
