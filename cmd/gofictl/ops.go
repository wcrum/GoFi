package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/internal/testkit/gofiapi"
	"github.com/wcrum/gofi/spec"
)

// runDelete removes named flows.
func runDelete(ctx context.Context, g *globals, args []string) error {
	fs := newFlagSet("delete", g)
	rest, err := parseInterspersed(fs, args, 0)
	if err != nil {
		return err
	}
	if len(rest) < 2 {
		return errors.New("usage: gofictl delete flow NAME [NAME...]")
	}
	switch strings.ToLower(rest[0]) {
	case "flow", "flows", "fl", "f":
	default:
		return fmt.Errorf("unknown resource %q (try: flow)", rest[0])
	}
	client := g.client()
	failed := 0
	for _, name := range rest[1:] {
		if err := client.Delete(ctx, name); err != nil {
			failed++
			fmt.Printf("flow/%s: %s\n", name, describe(err))
			continue
		}
		fmt.Printf("flow/%s deleted\n", name)
	}
	if failed > 0 {
		return fmt.Errorf("%d deletions failed", failed)
	}
	return nil
}

// runLifecycle starts, stops, or restarts named flows and prints the phase
// the controller reports right after.
func runLifecycle(ctx context.Context, g *globals, verb string, args []string) error {
	fs := newFlagSet(verb, g)
	wait := fs.Duration("wait", 0, "wait up to this long for the flow to reach running (start/restart) or stopped (stop)")
	rest, err := parseInterspersed(fs, args, 0)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("usage: gofictl %s NAME [NAME...]", verb)
	}
	client := g.client()
	target := "running"
	past := verb + "ed"
	if verb == "stop" {
		target = "stopped"
		past = "stopped"
	}
	failed := 0
	for _, name := range rest {
		var err error
		switch verb {
		case "start":
			err = client.Start(ctx, name)
		case "stop":
			err = client.Stop(ctx, name)
		default:
			err = client.Restart(ctx, name)
		}
		if err != nil {
			failed++
			fmt.Printf("flow/%s: %s\n", name, describe(err))
			continue
		}
		if *wait > 0 {
			waitCtx, cancel := context.WithTimeout(ctx, *wait)
			err = client.WaitPhase(waitCtx, name, target)
			cancel()
			if err != nil {
				failed++
				fmt.Printf("flow/%s %s; %s\n", name, past, err)
				continue
			}
		}
		phase := ""
		if status, err := client.Status(ctx, name); err == nil {
			phase = status.Phase
		}
		fmt.Printf("flow/%s %s (phase: %s)\n", name, past, dash(phase))
	}
	if failed > 0 {
		return fmt.Errorf("%d flows failed", failed)
	}
	return nil
}

// runHistory lists a flow's revisions, or prints one revision's definition.
func runHistory(ctx context.Context, g *globals, args []string) error {
	fs := newFlagSet("history", g)
	revision := fs.Int64("revision", 0, "print this revision's definition instead of the list")
	output := fs.String("o", "table", "output for a revision: yaml or json; table for the list")
	fs.StringVar(output, "output", "table", "output for a revision: yaml or json; table for the list")
	rest, err := parseInterspersed(fs, args, 0)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("usage: gofictl history NAME [--revision N -o yaml]")
	}
	name := rest[0]
	client := g.client()
	if *revision > 0 {
		rev, err := client.Revision(ctx, name, *revision)
		if err != nil {
			return fmt.Errorf("revision %d of %s: %s", *revision, name, describe(err))
		}
		if rev.Deleted {
			fmt.Printf("# revision %d deleted flow %s\n", rev.Revision, name)
			return nil
		}
		def, err := gofiapi.FromProto(rev.Definition)
		if err != nil {
			return err
		}
		if *output == "json" {
			return printJSON([]spec.FlowDefinition{def})
		}
		data, err := desired.EncodeFlow(def)
		if err != nil {
			return err
		}
		fmt.Print(string(data))
		return nil
	}
	revisions, err := client.Revisions(ctx, name)
	if err != nil {
		return fmt.Errorf("history of %s: %s", name, describe(err))
	}
	if len(revisions) == 0 {
		fmt.Println("No revisions found.")
		return nil
	}
	rows := make([][]string, 0, len(revisions))
	for _, rev := range revisions {
		created := "-"
		if rev.CreatedAt != nil {
			created = rev.CreatedAt.AsTime().Local().Format(time.RFC3339)
		}
		hash := rev.Hash
		if len(hash) > 12 {
			hash = hash[:12]
		}
		change := "saved"
		if rev.Deleted {
			change = "deleted"
		}
		rows = append(rows, []string{strconv.FormatInt(rev.Revision, 10), created, dash(rev.Source), dash(rev.Author), change, dash(hash), dash(rev.Message)})
	}
	fmt.Print(table([]string{"REVISION", "CREATED", "SOURCE", "AUTHOR", "CHANGE", "HASH", "MESSAGE"}, rows))
	return nil
}

// runRollback restores a revision through the normal save path.
func runRollback(ctx context.Context, g *globals, args []string) error {
	fs := newFlagSet("rollback", g)
	message := fs.String("m", "", "revision message")
	fs.StringVar(message, "message", "", "revision message")
	rest, err := parseInterspersed(fs, args, 0)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return errors.New("usage: gofictl rollback NAME REVISION")
	}
	name := rest[0]
	revision, err := strconv.ParseInt(rest[1], 10, 64)
	if err != nil || revision <= 0 {
		return fmt.Errorf("revision must be a positive integer, got %q", rest[1])
	}
	client := g.client()
	result, err := client.RollbackWithMessage(ctx, name, revision, *message)
	if err != nil {
		return fmt.Errorf("rollback %s to %d: %s", name, revision, describe(err))
	}
	if !result.Created {
		fmt.Printf("flow/%s unchanged (already matches revision %d)\n", name, revision)
		return nil
	}
	fmt.Printf("flow/%s rolled back to revision %d (new revision %d)\n", name, revision, result.Revision.GetRevision())
	return nil
}
