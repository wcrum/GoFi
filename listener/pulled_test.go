package listener

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wcrum/gofi/spec"
)

func TestFetchSourceRunsEachElementThroughThePipeline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"items":[{"level":"info","n":1},{"level":"debug","n":2}]}`)
	}))
	defer server.Close()
	def := spec.FlowDefinition{
		Name:    "pulled",
		Source:  spec.FlowSource{Fetch: &spec.FetchSource{HTTP: &spec.HTTPFetch{URL: server.URL, Every: "1m"}, Split: "$.items[*]"}},
		Framing: spec.Framing{Mode: "raw"},
		Parser:  spec.ParserCfg{Format: "json"},
		Stages:  []spec.StageSpec{{ID: "info", Kind: "filter", Type: "cel", Expression: `fields["level"] == "info"`}},
		Sinks:   []spec.SinkCfg{{Type: "stdout"}},
	}
	l, err := New(def)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	events := l.Tap.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)
	var passed, dropped int
	for passed+dropped < 2 {
		select {
		case ev := <-events:
			if ev.Dropped {
				dropped++
			} else if ev.Msg.Fields["n"] == float64(1) {
				passed++
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("passed %d dropped %d", passed, dropped)
		}
	}
	if passed != 1 || dropped != 1 {
		t.Fatalf("passed %d dropped %d", passed, dropped)
	}
}
