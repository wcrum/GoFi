package main

import (
	"testing"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/spec"
)

func TestCheckSourceFunction(t *testing.T) {
	functions := []*gofiv1.WorkerFunction{{Name: "poll", Kind: "fetcher"}, {Name: "feed", Kind: "streamer"}, {Name: "normalize", Kind: "transformer"}}
	for _, name := range []string{"poll", "feed"} {
		if err := checkSourceFunction(&spec.PythonSource{Library: "radar", Function: name}, functions); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"normalize", "missing"} {
		if err := checkSourceFunction(&spec.PythonSource{Library: "radar", Function: name}, functions); err == nil {
			t.Fatalf("%s accepted as a source", name)
		}
	}
}
