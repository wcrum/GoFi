// Package flows builds flow definitions for tests: minimal valid listen flows
// whose only output is an HTTP sink, so a test can count delivered messages
// by id (see testkit/traffic).
package flows

import (
	"fmt"

	"github.com/wcrum/gofi/spec"
)

// Option mutates a definition built by Listen.
type Option func(*spec.FlowDefinition)

// Listen is a TCP (or UDP) listen flow with newline framing and a JSON
// parser. Without a sink option it writes to stdout.
func Listen(name string, port int, opts ...Option) spec.FlowDefinition {
	def := spec.FlowDefinition{
		Name:    name,
		Source:  spec.FlowSource{Listen: &spec.ListenSource{Port: port, Network: "tcp"}},
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Sinks:   []spec.SinkCfg{{Type: "stdout"}},
	}
	for _, opt := range opts {
		opt(&def)
	}
	return def
}

// UDP switches the listen source to UDP.
func UDP() Option {
	return func(def *spec.FlowDefinition) { def.Source.Listen.Network = "udp" }
}

// QUIC switches the listen source to QUIC (over UDP on the same port). With
// no tls block the flow serves an ephemeral self-signed certificate, which
// the testkit generator accepts by default.
func QUIC() Option {
	return func(def *spec.FlowDefinition) { def.Source.Listen.Network = "quic" }
}

// QUICSink replaces the sinks with one QUIC sink writing newline-delimited
// payloads to url (quic://host:port), typically another flow's QUIC port.
// Payloads carry the parsed fields only (not the wrapped envelope), so the
// receiving flow parses the same JSON object the sender produced and a
// receiver downstream still sees the original id. The sink skips certificate
// verification because the peer flow serves an ephemeral self-signed
// certificate; SinkTLS replaces that for a flow with a real certificate.
func QUICSink(url string) Option {
	return func(def *spec.FlowDefinition) {
		def.Sinks = []spec.SinkCfg{{Type: spec.NetworkQUIC, URL: url, Delimiter: "\n", Encoding: &spec.Encoding{Format: "fields"}, TLS: &spec.TLSConfig{InsecureSkipVerify: true}}}
	}
}

// SinkTLS sets the TLS block of every sink (QUIC sinks).
func SinkTLS(cfg *spec.TLSConfig) Option {
	return func(def *spec.FlowDefinition) {
		for i := range def.Sinks {
			def.Sinks[i].TLS = cfg
		}
	}
}

// ListenTLS sets the listen source's TLS block (QUIC listen sources).
func ListenTLS(cfg *spec.TLSConfig) Option {
	return func(def *spec.FlowDefinition) { def.Source.Listen.TLS = cfg }
}

// HTTPSink replaces the sinks with one HTTP sink posting wrapped JSON
// ({"fields": ..., "meta": ...}) to url.
func HTTPSink(url string) Option {
	return func(def *spec.FlowDefinition) {
		def.Sinks = []spec.SinkCfg{{Type: "http", URL: url}}
	}
}

// Stopped declares the flow stopped.
func Stopped() Option {
	return func(def *spec.FlowDefinition) { def.State = spec.StateStopped }
}

// Service exposes the listen port through a Kubernetes Service.
func Service(kind string, nodePort int) Option {
	return func(def *spec.FlowDefinition) {
		def.Source.Listen.Service = &spec.ServiceExposure{Type: kind, NodePort: nodePort}
	}
}

// Stage appends a stage.
func Stage(stage spec.StageSpec) Option {
	return func(def *spec.FlowDefinition) { def.Stages = append(def.Stages, stage) }
}

// Transform appends a "set" transform stage, a cheap way to produce an edit
// that changes the runtime definition without touching the port.
func Transform(id, field, valueExpr string) Option {
	return Stage(spec.StageSpec{ID: id, Kind: "transform", TransformOp: "set", Field: field, ValueExpr: valueExpr})
}

// Many returns n listen flows named <prefix>-0001.. on consecutive ports
// starting at basePort, each built with opts.
func Many(prefix string, n, basePort int, opts ...Option) []spec.FlowDefinition {
	out := make([]spec.FlowDefinition, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Listen(fmt.Sprintf("%s-%04d", prefix, i), basePort+i, opts...))
	}
	return out
}

// Names lists the flow names of defs.
func Names(defs []spec.FlowDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Name)
	}
	return out
}
