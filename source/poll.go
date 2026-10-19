package source

import (
	"context"
	"log/slog"
	"time"

	"github.com/wcrum/gofi/observability"
)

// pollLoop calls poll now and then every interval until ctx ends. Polls run
// one at a time, so a slow poll delays the next one instead of overlapping
// it. Failures are counted, logged, and retried at the next interval.
func pollLoop(ctx context.Context, flow, kind, target string, every time.Duration, poll func(context.Context) error) error {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if err := poll(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			observability.SourceEvent(flow, kind, "error")
			observability.SourceFailure(flow, err)
			slog.Warn("fetch failed; retrying at the next interval", "flow", flow, "source", kind, "target", target, "error", err, "every", every)
		} else {
			observability.SourceEvent(flow, kind, "ok")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
