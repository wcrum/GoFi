package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/internal/testkit/gofiapi"
	"github.com/wcrum/gofi/spec"
)

type fileList []string

func (f *fileList) String() string     { return fmt.Sprint([]string(*f)) }
func (f *fileList) Set(v string) error { *f = append(*f, v); return nil }

// runApply reads Flow resources from files and saves each one that differs
// from what the controller has, reporting created / configured / unchanged.
func runApply(ctx context.Context, g *globals, args []string) error {
	fs := newFlagSet("apply", g)
	var files fileList
	fs.Var(&files, "f", "file to apply (repeatable; - for stdin)")
	fs.Var(&files, "filename", "file to apply (repeatable; - for stdin)")
	dryRun := fs.Bool("dry-run", false, "validate on the controller without saving")
	message := fs.String("m", "", "revision message recorded in the flow's history")
	fs.StringVar(message, "message", "", "revision message recorded in the flow's history")
	rest, err := parseInterspersed(fs, args, 0)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("apply takes no positional arguments (got %q); use -f FILE", rest[0])
	}
	if len(files) == 0 {
		return errors.New("apply requires -f FILE")
	}
	var defs []spec.FlowDefinition
	for _, path := range files {
		items, err := readFlows(path)
		if err != nil {
			return err
		}
		defs = append(defs, items...)
	}
	if len(defs) == 0 {
		return errors.New("no Flow resources found")
	}
	client := g.client()
	failed := 0
	for _, def := range defs {
		outcome, warnings, err := applyOne(ctx, client, def, *dryRun, *message)
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "warning: flow/%s: %s\n", def.Name, w)
		}
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "error: flow/%s: %s\n", def.Name, describe(err))
			continue
		}
		fmt.Printf("flow/%s %s\n", def.Name, outcome)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d flows failed", failed, len(defs))
	}
	return nil
}

// readFlows decodes one file with the file store's rules.
func readFlows(path string) ([]spec.FlowDefinition, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	defs, err := desired.DecodeFlows(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return defs, nil
}

func applyOne(ctx context.Context, client *gofiapi.Client, def spec.FlowDefinition, dryRun bool, message string) (string, []string, error) {
	existing, err := client.Get(ctx, def.Name)
	exists := err == nil
	if err != nil && !isNotFound(err) {
		return "", nil, err
	}
	if exists && sameDefinition(existing, def) {
		if dryRun {
			return "unchanged (dry run)", nil, nil
		}
		return "unchanged", nil, nil
	}
	outcome := "configured"
	if !exists {
		outcome = "created"
	}
	if dryRun {
		warnings, err := client.Validate(ctx, def)
		return outcome + " (dry run)", warnings, err
	}
	warnings, err := client.Apply(ctx, def, message)
	return outcome, warnings, err
}

// sameDefinition compares the controller's stored definition with a local
// one after both pass through the API's protobuf shape, so field defaults
// and omitted keys do not count as changes.
func sameDefinition(stored, local spec.FlowDefinition) bool {
	normalized, err := roundTrip(local)
	if err != nil {
		return false
	}
	a, errA := json.Marshal(stored)
	b, errB := json.Marshal(normalized)
	return errA == nil && errB == nil && string(a) == string(b)
}

func roundTrip(def spec.FlowDefinition) (spec.FlowDefinition, error) {
	flow, err := gofiapi.ToProto(def)
	if err != nil {
		return def, err
	}
	return gofiapi.FromProto(flow)
}
