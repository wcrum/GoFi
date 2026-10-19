package driver

import (
	"testing"

	"github.com/wcrum/gofi/spec"
)

// A Python source's function connects out from its library's worker (under
// the library's egress rules), so the flow runtime gets no source egress and
// the connectivity analysis lists no source path.
func TestPythonSourceNeedsNoFlowEgress(t *testing.T) {
	src := spec.FlowSource{Python: &spec.PythonSource{Library: "radar", Function: "poll"}}
	if entry := sourceEgress("radar-flow", src); entry != nil {
		t.Fatalf("source egress = %v", entry)
	}
	if more := moreSourceEgress("radar-flow", src); len(more) != 0 {
		t.Fatalf("more egress = %v", more)
	}
	if url := sourceURL(src); url != "" {
		t.Fatalf("source URL = %q", url)
	}
}
