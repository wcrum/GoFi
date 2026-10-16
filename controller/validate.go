package controller

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wcrum/gofi/filter"
	"github.com/wcrum/gofi/framing"
	"github.com/wcrum/gofi/parser"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
	"k8s.io/apimachinery/pkg/util/validation"
)

// ValidateDefinition checks the complete portable resource contract without
// binding ports, contacting destinations, or reading listener-mounted files.
func ValidateDefinition(def spec.FlowDefinition) error {
	if _, err := json.Marshal(def); err != nil {
		return fmt.Errorf("invalid flow configuration: %w", err)
	}
	if errs := validation.IsDNS1123Subdomain(def.Name); len(errs) != 0 {
		return fmt.Errorf("name must be a valid Kubernetes resource name: %s", strings.Join(errs, "; "))
	}
	// Every Python step (framer, decoders, parser, stages, encoders, sinks)
	// must name a function, or carry inline source where that is allowed.
	for _, use := range def.PythonRefs() {
		if err := use.Ref.Validate(); err != nil {
			return fmt.Errorf("%s: %w", use.Path, err)
		}
	}
	if def.Source.Listen != nil && def.Source.Listening().Port == 0 {
		return fmt.Errorf("source.listen.port is required")
	}
	if err := def.Source.Validate(); err != nil {
		return err
	}
	if def.Framing.MaxBytes < 0 {
		return fmt.Errorf("framing.maxBytes must not be negative")
	}
	if def.Framing.Mode == "length-prefix" && def.Framing.PrefixLen != 0 && def.Framing.PrefixLen != 2 && def.Framing.PrefixLen != 4 {
		return fmt.Errorf("framing.prefixLen must be 2 or 4")
	}
	if _, err := filter.NewChain(def.Stages); err != nil {
		return err
	}
	if _, err := parser.New(def.Parser); err != nil {
		return err
	}
	if _, err := framing.New(def.Framing, strings.NewReader("")); err != nil {
		return err
	}
	for i, cfg := range def.Sinks {
		if err := sink.Validate(cfg); err != nil {
			return fmt.Errorf("sink %d: %w", i, err)
		}
		if err := cfg.Encoding.Validate(); err != nil {
			return fmt.Errorf("sink %d: %w", i, err)
		}
	}
	if def.DeadLetterSink != nil {
		if err := sink.Validate(*def.DeadLetterSink); err != nil {
			return fmt.Errorf("dead-letter sink: %w", err)
		}
		if err := def.DeadLetterSink.Encoding.Validate(); err != nil {
			return fmt.Errorf("dead-letter sink: %w", err)
		}
	}
	return nil
}

// Validate also enforces the capabilities of the selected deployment driver.
func (c *Controller) Validate(def spec.FlowDefinition) error {
	return c.ValidateWith(def, c.library())
}

// validateResolved validates a definition whose CEL library references have
// already been resolved to expression text.
func (c *Controller) validateResolved(def spec.FlowDefinition) error {
	if err := ValidateDefinition(def); err != nil {
		return err
	}
	if def.Source.Listening().Service != nil && c.drv.Kind() != "kubernetes" {
		return fmt.Errorf("source.service is supported only by the Kubernetes driver; use the host port in %s mode", c.drv.Kind())
	}
	sinks := append([]spec.SinkCfg(nil), def.Sinks...)
	if def.DeadLetterSink != nil {
		sinks = append(sinks, *def.DeadLetterSink)
	}
	for _, sink := range sinks {
		if sink.Kubernetes == nil {
			continue
		}
		if c.drv.Kind() != "kubernetes" {
			return fmt.Errorf("Kubernetes selector targets require the Kubernetes driver")
		}
		if scoped, ok := c.drv.(interface{ AllowsTargetNamespace(string) error }); ok {
			if err := scoped.AllowsTargetNamespace(sink.Kubernetes.Namespace); err != nil {
				return err
			}
		}
	}
	return nil
}
