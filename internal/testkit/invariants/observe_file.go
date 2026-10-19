package invariants

import (
	"context"
	"fmt"

	"github.com/wcrum/gofi/desired"
	"github.com/wcrum/gofi/history"
	"github.com/wcrum/gofi/spec"
)

// ObserveFile reads the declared flow set from a flow file and its history
// directory (binary and Docker deployments). It sees no runtimes; combine it
// with ObserveAPI or ObserveDocker, or check it with RuntimesExpected=false.
func ObserveFile(ctx context.Context, configPath, historyDir string) (World, error) {
	w := World{Observed: "file:" + configPath}
	store := &desired.File{Path: configPath}
	defs, err := store.Load(ctx)
	if err != nil {
		return w, fmt.Errorf("flow file: %w", err)
	}
	w.Desired = defs
	if historyDir == "" {
		return w, nil
	}
	hist := &history.File{Dir: historyDir}
	flows, err := hist.Flows(ctx)
	if err != nil {
		return w, fmt.Errorf("history: %w", err)
	}
	w.History = map[string][]Revision{}
	for _, flow := range flows {
		revs, err := hist.List(ctx, flow)
		if err != nil {
			return w, fmt.Errorf("history %s: %w", flow, err)
		}
		w.History[flow] = convertRevisions(revs)
	}
	return w, nil
}

func convertRevisions(revs []history.Revision) []Revision {
	out := make([]Revision, 0, len(revs))
	for _, rev := range revs {
		out = append(out, Revision{Number: rev.Number, Hash: rev.Hash, Deleted: rev.Deleted, Def: rev.Definition})
	}
	return out
}

// Merge overlays b onto a: runtimes, services, secrets, bindings, orphans,
// metrics, and readiness come from b when a has none; desired and history are
// taken from whichever has them.
func Merge(a, b World) World {
	out := a
	if len(out.Desired) == 0 {
		out.Desired = b.Desired
	}
	if len(out.Invalid) == 0 {
		out.Invalid = b.Invalid
	}
	if len(out.Libraries) == 0 {
		out.Libraries = b.Libraries
	}
	if len(out.History) == 0 {
		out.History = b.History
	}
	out.Runtimes = append(out.Runtimes, b.Runtimes...)
	out.Services = append(out.Services, b.Services...)
	out.Secrets = append(out.Secrets, b.Secrets...)
	out.Orphans = append(out.Orphans, b.Orphans...)
	if out.Bindings == nil {
		out.Bindings = b.Bindings
	}
	if out.Allows == nil {
		out.Allows = b.Allows
	}
	if out.Metrics == nil {
		out.Metrics = map[string]bool{}
	}
	for k, v := range b.Metrics {
		out.Metrics[k] = v
	}
	if out.Ready == nil {
		out.Ready = b.Ready
	}
	if out.APIAnswers == nil {
		out.APIAnswers = b.APIAnswers
	}
	out.Observed = a.Observed + "+" + b.Observed
	return out
}

// DesiredFrom builds a World holding only a declared flow set (for checks
// against an in-memory controller or driver).
func DesiredFrom(defs []spec.FlowDefinition) World {
	return World{Desired: defs, Observed: "memory"}
}
