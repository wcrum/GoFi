package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// FunctionKinds lists the @gofi decorators in flow order; each kind plugs into
// one flow step.
var FunctionKinds = []string{"framer", "decoder", "parser", "transformer", "encoder", "sink"}

// PythonRef identifies a named function in a durable, isolated Python library.
// Runtime addresses and credentials are resolved privately by the controller.
type PythonRef struct {
	Library  string `json:"library"`
	Function string `json:"function"`
	// InlineSource is Python authored directly in a flow stage. The controller
	// folds a listener's inline sources into one managed library and sets
	// Library/Function; the source never runs in the controller.
	InlineSource string `json:"inlineSource,omitempty"`
	// Config is per-node configuration passed as keyword arguments.
	Config map[string]any `json:"config,omitempty"`
}

func (r *PythonRef) Validate() error {
	if r == nil {
		return fmt.Errorf("python.library and python.function are required")
	}
	if r.InlineSource != "" {
		// Unresolved inline refs (editor simulations) run in a scratch worker;
		// stored listeners carry the managed library resolved by the controller.
		return ValidateInlineSource(r.InlineSource)
	}
	if r.Library == "" || r.Function == "" {
		return fmt.Errorf("python.library and python.function are required")
	}
	return nil
}

// MaxInlineSourceBytes bounds one inline stage; a listener's managed library
// must still fit the 64 KiB library limit.
const MaxInlineSourceBytes = 16384

var inlineEntryPoint = regexp.MustCompile(`(?m)^(async\s+)?def\s+transform\s*\(`)

// ValidateInlineSource checks the structural contract of an inline stage
// without parsing or executing Python: a top-level `def transform(...)`.
func ValidateInlineSource(source string) error {
	if len(source) > MaxInlineSourceBytes {
		return fmt.Errorf("inline Python exceeds %d bytes", MaxInlineSourceBytes)
	}
	if !inlineEntryPoint.MatchString(source) {
		return fmt.Errorf("inline Python must define a top-level `def transform(fields, meta=None, **config)`")
	}
	return nil
}

var librarySlug = regexp.MustCompile(`[^a-z0-9]+`)

// ManagedLibraryName is the deterministic auto-managed library that holds a
// listener's inline stages: flow-<slug>-<hash>, within the 40-character
// library name limit and unique per listener name.
func ManagedLibraryName(listener string) string {
	sum := sha256.Sum256([]byte(listener))
	slug := strings.Trim(librarySlug.ReplaceAllString(strings.ToLower(listener), "-"), "-")
	if len(slug) > 26 {
		slug = strings.TrimRight(slug[:26], "-")
	}
	if slug == "" {
		return "flow-" + hex.EncodeToString(sum[:4])
	}
	return "flow-" + slug + "-" + hex.EncodeToString(sum[:4])
}

// ManagedBy is the ownership marker of a listener's managed library.
func ManagedBy(listener string) string { return "flow:" + listener }

// InlineStages returns the stages carrying inline Python source.
func (d FlowDefinition) InlineStages() []StageSpec {
	var out []StageSpec
	for _, s := range d.Stages {
		if s.Python != nil && s.Python.InlineSource != "" {
			out = append(out, s)
		}
	}
	return out
}

// ResolveInlinePython points every inline stage at the listener's managed
// library (function = stage ID). The inline source remains the source of
// truth; the resolved names are derived and recomputed on every load.
func (d *FlowDefinition) ResolveInlinePython() {
	for i := range d.Stages {
		if ref := d.Stages[i].Python; ref != nil && ref.InlineSource != "" {
			ref.Library, ref.Function = ManagedLibraryName(d.Name), d.Stages[i].ID
		}
	}
}

// UnresolveInlinePython clears derived library names so each inline stage
// runs its own current source (used by simulations of unsaved edits).
func (d *FlowDefinition) UnresolveInlinePython() {
	for i := range d.Stages {
		if ref := d.Stages[i].Python; ref != nil && ref.InlineSource != "" {
			ref.Library, ref.Function = "", d.Stages[i].ID
		}
	}
}

// PythonRef paths name every Python function a flow uses, with the kind of
// function each must be ("framer", "decoder", "parser", "transformer",
// "encoder", or "sink").
type PythonUse struct {
	Path string
	Kind string
	Ref  *PythonRef
}

// PythonRefs lists every Python function the flow references, in pipeline
// order: framer, decoders, parser, stages, then each sink's encoders and sink.
func (d FlowDefinition) PythonRefs() []PythonUse {
	var uses []PythonUse
	add := func(path, kind string, ref *PythonRef) {
		if ref != nil {
			uses = append(uses, PythonUse{Path: path, Kind: kind, Ref: ref})
		}
	}
	add("framing.python", "framer", d.Framing.Python)
	for i, step := range d.Parser.Decoders {
		add(fmt.Sprintf("parser.decoders[%d].python", i), "decoder", step.Python)
	}
	add("parser.python", "parser", d.Parser.Python)
	for i, stage := range d.Stages {
		add(fmt.Sprintf("stages[%d].python", i), "transformer", stage.Python)
	}
	sink := func(prefix string, s SinkCfg) {
		if s.Encoding != nil {
			for j, step := range s.Encoding.Encoders {
				add(fmt.Sprintf("%s.encoding.encoders[%d].python", prefix, j), "encoder", step.Python)
			}
		}
		add(prefix+".python", "sink", s.Python)
	}
	for i, s := range d.Sinks {
		sink(fmt.Sprintf("sinks[%d]", i), s)
	}
	if d.DeadLetterSink != nil {
		sink("deadLetterSink", *d.DeadLetterSink)
	}
	return uses
}

// ValidateInlinePlacement rejects inline source outside processing stages.
func (d FlowDefinition) ValidateInlinePlacement() error {
	for _, use := range d.PythonRefs() {
		if use.Kind != "transformer" && use.Ref.InlineSource != "" {
			return fmt.Errorf("%s: inline Python is supported only for processing stages; use a library function", use.Path)
		}
	}
	return nil
}

func (d FlowDefinition) UsesPython() bool { return len(d.PythonRefs()) > 0 }
