package spec

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Python source function kinds (docs/flows.md, R4).
const (
	KindFetcher  = "fetcher"
	KindStreamer = "streamer"
)

// SourceFunctionKinds are the decorators a source.python function may use.
var SourceFunctionKinds = []string{KindFetcher, KindStreamer}

// PythonSource runs a @gofi.fetcher (polled) or @gofi.streamer (long-lived)
// library function as the flow's source. The function executes in its
// library's isolated worker under that library's egress allowlist; the flow
// runtime only calls the worker. Whether it polls or streams follows the
// function's decorator.
type PythonSource struct {
	Library  string         `json:"library"`
	Function string         `json:"function"`
	Config   map[string]any `json:"config,omitempty"`
	// Every overrides a fetcher's poll interval (Go duration, at least 1s).
	// Empty uses the decorator's every, else DefaultFetchEvery.
	Every string `json:"every,omitempty"`
}

// MaxPythonSourceConfigBytes bounds source.python.config (JSON).
const MaxPythonSourceConfigBytes = 64 << 10

// Validate checks the reference; the function's kind is checked against the
// library's registry where one is available (controller save, runtime start).
func (p *PythonSource) Validate() error {
	if strings.TrimSpace(p.Library) == "" {
		return fmt.Errorf("source.python.library is required")
	}
	if strings.TrimSpace(p.Function) == "" {
		return fmt.Errorf("source.python.function is required")
	}
	if p.Every != "" {
		every, err := time.ParseDuration(p.Every)
		if err != nil || every < time.Second {
			return fmt.Errorf("source.python.every must be a duration of at least 1s")
		}
	}
	if len(p.Config) > 0 {
		data, err := json.Marshal(p.Config)
		if err != nil {
			return fmt.Errorf("source.python.config: %w", err)
		}
		if len(data) > MaxPythonSourceConfigBytes {
			return fmt.Errorf("source.python.config exceeds %d bytes", MaxPythonSourceConfigBytes)
		}
	}
	return nil
}

// Ref is the source function as a PythonRef, so binding resolution treats it
// like every other Python step of the flow.
func (p *PythonSource) Ref() *PythonRef {
	return &PythonRef{Library: p.Library, Function: p.Function, Config: p.Config}
}

// PollEvery is a fetcher's interval: the source's every, else the
// decorator's (fallback), else DefaultFetchEvery.
func (p *PythonSource) PollEvery(decorator string) time.Duration {
	for _, value := range []string{p.Every, decorator} {
		if every, err := time.ParseDuration(strings.TrimSpace(value)); err == nil && every >= time.Second {
			return every
		}
	}
	return DefaultFetchEvery
}

// CheckSourceKind reports whether kind may back source.python.
func CheckSourceKind(library, function, kind string) error {
	if kind == KindFetcher || kind == KindStreamer {
		return nil
	}
	return fmt.Errorf("source.python: %s.%s is a %s; a source needs a @gofi.fetcher or @gofi.streamer", library, function, kind)
}
