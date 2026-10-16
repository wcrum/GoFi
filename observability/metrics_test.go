package observability

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrometheusScrapeIncludesPipelineAndRuntimeMetrics(t *testing.T) {
	Frame("metrics-test", 17)
	ParseError("metrics-test")
	Rejected("metrics-test", "accepted-status")
	SinkDispatch("metrics-test", "http", time.Millisecond, nil)
	SinkDispatch("metrics-test", "http", 2*time.Millisecond, errors.New("unavailable"))
	r := httptest.NewRecorder()
	Handler().ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	body := r.Body.String()
	for _, want := range []string{`gofi_frames_received_total{listener="metrics-test"} 1`, `gofi_source_bytes_total{listener="metrics-test"} 17`, `gofi_parser_errors_total{listener="metrics-test"} 1`, `gofi_messages_rejected_total{listener="metrics-test",stage="accepted-status"} 1`, `gofi_sink_dispatch_total{listener="metrics-test",outcome="success",sink_type="http"} 1`, `gofi_sink_dispatch_total{listener="metrics-test",outcome="error",sink_type="http"} 1`, `gofi_sink_dispatch_duration_seconds_count{listener="metrics-test",sink_type="http"} 2`, `go_goroutines`, `process_cpu_seconds_total`} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %s", want)
		}
	}
}
