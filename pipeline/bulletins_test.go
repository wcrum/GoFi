package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

type failingSink struct{}

func (failingSink) Send(*message.Message) error { return errors.New("connection refused") }
func (failingSink) Close() error                { return nil }

// Parse errors and sink failures become bulletins aggregated by component and
// message: repeats raise the count of one entry.
func TestPipelineReportsBulletins(t *testing.T) {
	def := spec.FlowDefinition{Name: "bulletin-flow", Framing: spec.Framing{Mode: "line"}, Parser: spec.ParserCfg{Format: "json"}, Sinks: []spec.SinkCfg{{Type: "stdout"}}}
	observability.ClearBulletins(def.Name)
	chain, err := filter.NewChain(nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(def, strings.NewReader("not json\nnot json\n{\"ok\":1}\n"), []sink.Sink{failingSink{}}, nil, chain, NewTap())
	if err != nil {
		t.Fatal(err)
	}
	p.Run(context.Background(), "test")

	byComponent := map[string]observability.Bulletin{}
	for _, b := range observability.Bulletins(def.Name) {
		byComponent[b.Component] = b
	}
	parse, ok := byComponent[observability.ComponentParser]
	if !ok || parse.Severity != observability.SeverityWarning || parse.Count != 2 {
		t.Fatalf("parser bulletin = %+v", parse)
	}
	failed, ok := byComponent["sink:0"]
	if !ok || failed.Severity != observability.SeverityError || failed.Count != 3 || failed.Message != "connection refused" {
		t.Fatalf("sink bulletin = %+v (all %+v)", failed, byComponent)
	}
	if failed.ID != observability.BulletinID(def.Name, "sink:0", "connection refused") {
		t.Fatal("bulletin ID is not stable")
	}
}

// The board keeps at most MaxBulletinsPerFlow distinct problems per flow.
func TestBulletinBoardIsBounded(t *testing.T) {
	const flow = "bounded-flow"
	observability.ClearBulletins(flow)
	for i := range observability.MaxBulletinsPerFlow + 50 {
		observability.Report(flow, observability.SeverityError, observability.ComponentStage("s"), strings.Repeat("x", i+1))
	}
	if got := len(observability.Bulletins(flow)); got != observability.MaxBulletinsPerFlow {
		t.Fatalf("board holds %d bulletins", got)
	}
}

func TestStageComponentNamesTheStage(t *testing.T) {
	if got := stageComponent(errors.New(`stage "enrich": no such key`)); got != "stage:enrich" {
		t.Fatal(got)
	}
	if got := stageComponent(errors.New("boom")); got != "stages" {
		t.Fatal(got)
	}
}
