package invariants

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/wcrum/gofi/internal/testkit/gofiapi"
	"github.com/wcrum/gofi/spec"
)

// APIOptions tune ObserveAPI.
type APIOptions struct {
	// WithDefinitions fetches every revision's definition (one RPC each) so
	// the history hash check can run; summaries alone check monotonicity and
	// the head hash.
	WithDefinitions bool
	// SkipHistory does not query FlowHistoryService (disabled deployments).
	SkipHistory bool
}

// ObserveAPI sees the deployment the way the portal does: stored flows,
// each flow's runtime status, revision history, /readyz, and /metrics.
// Runtimes come from GetRuntimeStatus, so this observer sees at most one
// runtime per flow; pair it with ObserveDocker/ObserveKube to catch
// duplicates and orphans.
func ObserveAPI(ctx context.Context, client *gofiapi.Client, opts APIOptions) (World, error) {
	w := World{Observed: "api:" + client.BaseURL, Metrics: map[string]bool{}, History: map[string][]Revision{}}
	ready := client.Ready(ctx)
	w.Ready = &ready
	defs, err := client.List(ctx)
	answers := err == nil
	w.APIAnswers = &answers
	if err != nil {
		return w, fmt.Errorf("ListFlows: %w", err)
	}
	statuses, err := client.ListStatus(ctx)
	if err != nil {
		return w, fmt.Errorf("ListFlowStatus: %w", err)
	}
	invalid := map[string]bool{}
	for _, status := range statuses {
		if status.Phase == "invalid" {
			invalid[status.Name] = true
		}
	}
	for _, def := range defs {
		if invalid[def.Name] {
			w.Invalid = append(w.Invalid, def.Name)
			continue
		}
		w.Desired = append(w.Desired, def)
	}
	for _, def := range defs {
		info, err := client.Runtime(ctx, def.Name)
		if connect.CodeOf(err) == connect.CodeNotFound {
			continue // no runtime (stopped, or not started yet)
		}
		if err != nil {
			return w, fmt.Errorf("GetRuntimeStatus %s: %w", def.Name, err)
		}
		if info == nil || (info.Status == "" && info.Id == "") {
			continue
		}
		status := info.Status
		if status == "failed" || status == "invalid" {
			// The controller reports a start failure as a pseudo-runtime.
			continue
		}
		w.Runtimes = append(w.Runtimes, Runtime{Flow: def.Name, ID: firstNonEmpty(info.Id, info.Kind+"/"+def.Name), Status: status, Node: info.Node})
	}
	if !opts.SkipHistory {
		for _, def := range defs {
			revs, err := client.Revisions(ctx, def.Name)
			if err != nil {
				return w, fmt.Errorf("ListFlowRevisions %s: %w", def.Name, err)
			}
			list := make([]Revision, 0, len(revs))
			for i := len(revs) - 1; i >= 0; i-- { // API lists newest first
				rev := revs[i]
				item := Revision{Number: rev.Revision, Hash: rev.Hash, Deleted: rev.Deleted}
				if opts.WithDefinitions && !rev.Deleted {
					full, err := client.Revision(ctx, def.Name, rev.Revision)
					if err != nil {
						return w, fmt.Errorf("GetFlowRevision %s/%d: %w", def.Name, rev.Revision, err)
					}
					if full.Definition != nil {
						d, err := gofiapi.FromProto(full.Definition)
						if err != nil {
							return w, err
						}
						item.Def = &d
					}
				}
				list = append(list, item)
			}
			w.History[def.Name] = list
		}
	}
	_, err = client.Metrics(ctx)
	w.Metrics[client.BaseURL+"/metrics"] = err == nil
	return w, nil
}

// WaitConverged polls ObserveAPI + Check until no violation remains or ctx
// ends; it returns the last violations.
func WaitConverged(ctx context.Context, client *gofiapi.Client, apiOpts APIOptions, opts Options, extra func(World) World) ([]Violation, error) {
	var last []Violation
	for {
		w, err := ObserveAPI(ctx, client, apiOpts)
		if err == nil {
			if extra != nil {
				w = extra(w)
			}
			last = Check(w, opts)
			if len(last) == 0 {
				return nil, nil
			}
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return last, err
			}
			return last, fmt.Errorf("not converged: %s", Summary(last))
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// DesiredNames is a convenience for reports.
func DesiredNames(defs []spec.FlowDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Name)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
