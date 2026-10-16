package pipeline

import (
	"context"
	"errors"
	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type failedMetricSink struct{}

func (*failedMetricSink) Send(*message.Message) error {
	return errors.New("test destination unavailable")
}
func (*failedMetricSink) Close() error { return nil }
func TestPipelineMetricsMeasureFramesDropsAndDispatchCalls(t *testing.T) {
	def := spec.ListenerDefinition{Name: "pipeline-metrics-test", Framing: spec.Framing{Mode: "line"}, Parser: spec.ParserCfg{Format: "json", ErrorPolicy: "drop"}, Stages: []spec.StageSpec{{ID: "successful", Kind: "filter", Type: "cel", Expression: `fields["status"] < 300`}}, Sinks: []spec.SinkCfg{{Type: "stdout"}, {Type: "http"}}, DeadLetterSink: &spec.SinkCfg{Type: "stdout"}}
	chain, e := filter.NewChain(def.Stages)
	if e != nil {
		t.Fatal(e)
	}
	frames := []string{`{"status":201}`, `{"status":503}`, `invalid-json`}
	p, e := New(def, strings.NewReader(strings.Join(frames, "\n")+"\n"), []sink.Sink{&fakeSink{}, &failedMetricSink{}}, &fakeSink{}, chain, NewTap())
	if e != nil {
		t.Fatal(e)
	}
	p.Run(context.Background(), "test-source")
	r := httptest.NewRecorder()
	observability.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	bytes := 0
	for _, frame := range frames {
		bytes += len(frame)
	}
	for _, want := range []string{`gofi_frames_received_total{listener="pipeline-metrics-test"} 3`, `gofi_source_bytes_total{listener="pipeline-metrics-test"} ` + strconv.Itoa(bytes), `gofi_parser_errors_total{listener="pipeline-metrics-test"} 1`, `gofi_messages_rejected_total{listener="pipeline-metrics-test",stage="successful"} 1`, `gofi_messages_rejected_total{listener="pipeline-metrics-test",stage="parser"} 1`, `gofi_sink_dispatch_total{listener="pipeline-metrics-test",outcome="success",sink_type="stdout"} 3`, `gofi_sink_dispatch_total{listener="pipeline-metrics-test",outcome="error",sink_type="http"} 1`} {
		if !strings.Contains(r.Body.String(), want) {
			t.Errorf("metric absent: %s", want)
		}
	}
}
