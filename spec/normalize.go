package spec

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// Normalize upgrades a definition read in an older shape to the current one
// in place. Today that is the decode step: parser.decoders and
// parser.maxDecodedBytes move to decode.decoders and decode.maxDecodedBytes.
// A definition that sets both locations must agree on each value; when they
// differ Normalize returns an error rather than guess. Normalize is
// idempotent, and every read path (stores, the API, history, runtimes) runs
// it, so a definition is only ever written in the current shape.
func (d *FlowDefinition) Normalize() error {
	legacyDecoders, legacyMax := d.Parser.LegacyDecoders, d.Parser.LegacyMaxDecodedBytes
	d.Parser.LegacyDecoders, d.Parser.LegacyMaxDecodedBytes = nil, 0
	if len(legacyDecoders) > 0 || legacyMax != 0 {
		decode := DecodeCfg{}
		if d.Decode != nil {
			decode = *d.Decode
		}
		if len(legacyDecoders) > 0 {
			if len(decode.Decoders) > 0 && !reflect.DeepEqual(decode.Decoders, legacyDecoders) {
				d.Parser.LegacyDecoders, d.Parser.LegacyMaxDecodedBytes = legacyDecoders, legacyMax
				return fmt.Errorf("parser.decoders is deprecated and differs from decode.decoders; set only decode.decoders")
			}
			decode.Decoders = legacyDecoders
		}
		if legacyMax != 0 {
			if decode.MaxDecodedBytes != 0 && decode.MaxDecodedBytes != legacyMax {
				d.Parser.LegacyDecoders, d.Parser.LegacyMaxDecodedBytes = legacyDecoders, legacyMax
				return fmt.Errorf("parser.maxDecodedBytes is deprecated and differs from decode.maxDecodedBytes; set only decode.maxDecodedBytes")
			}
			decode.MaxDecodedBytes = legacyMax
		}
		d.Decode = &decode
	}
	if d.Decode != nil && len(d.Decode.Decoders) == 0 && d.Decode.MaxDecodedBytes == 0 {
		d.Decode = nil
	}
	if d.Python != nil && *d.Python == (FlowPython{}) {
		d.Python = nil
	}
	return nil
}

// DecodeSteps returns the flow's decode step configuration (zero when unset).
func (d FlowDefinition) DecodeSteps() DecodeCfg {
	if d.Decode == nil {
		return DecodeCfg{}
	}
	return *d.Decode
}

// DefaultLibrary is the flow-level default Python library ("" when unset).
func (d FlowDefinition) DefaultLibrary() string {
	if d.Python == nil {
		return ""
	}
	return d.Python.Library
}

// WithDefaultLibrary returns an independent copy of d in which every Python
// reference that names a function without a library (and carries no inline
// source) uses the flow's python.library. Without a default it returns d
// unchanged. Stored and displayed definitions keep the authored form; the
// copy is what validation, simulation, and runtimes use.
func (d FlowDefinition) WithDefaultLibrary() FlowDefinition {
	lib := d.DefaultLibrary()
	if lib == "" {
		return d
	}
	needed := false
	for _, use := range d.PythonRefs() {
		if use.Ref.Library == "" && use.Ref.InlineSource == "" && use.Kind != "source" {
			needed = true
			break
		}
	}
	if !needed {
		return d
	}
	// A JSON round trip deep-copies every PythonRef, so filling libraries
	// never writes through pointers shared with the caller's definition.
	data, err := json.Marshal(d)
	if err != nil {
		return d
	}
	var out FlowDefinition
	if err := json.Unmarshal(data, &out); err != nil {
		return d
	}
	for _, use := range out.PythonRefs() {
		if use.Kind == "source" {
			continue // a Python source always names its library
		}
		if use.Ref.Library == "" && use.Ref.InlineSource == "" {
			use.Ref.Library = lib
		}
	}
	return out
}

// Prepared is the definition runtimes and simulations execute: normalized,
// with the default Python library applied.
func (d FlowDefinition) Prepared() (FlowDefinition, error) {
	if err := d.Normalize(); err != nil {
		return d, err
	}
	return d.WithDefaultLibrary(), nil
}
