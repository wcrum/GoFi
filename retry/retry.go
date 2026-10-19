// Package retry is the capped-exponential-backoff policy GoFi uses for
// anything that talks to the network from an intermittently connected host:
// sink deliveries, pulled sources, outbound metrics. It never gives up on
// its own; the caller's context decides when to stop, so a sink keeps
// retrying for as long as the flow runs and DNS or link loss is just
// another transient failure.
package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// Policy describes a backoff schedule.
type Policy struct {
	// Initial is the first delay (default 200ms).
	Initial time.Duration
	// Max caps the delay (default 30s).
	Max time.Duration
	// Multiplier grows the delay each attempt (default 2).
	Multiplier float64
	// Jitter is the +/- fraction applied to each delay (default 0.2) so a
	// fleet of gateways recovering from the same outage does not reconnect
	// in lock step.
	Jitter float64
	// MaxAttempts bounds attempts (0 = unlimited; only the context stops Do).
	MaxAttempts int
}

func (p Policy) withDefaults() Policy {
	if p.Initial <= 0 {
		p.Initial = 200 * time.Millisecond
	}
	if p.Max <= 0 {
		p.Max = 30 * time.Second
	}
	if p.Max < p.Initial {
		p.Max = p.Initial
	}
	if p.Multiplier < 1 {
		p.Multiplier = 2
	}
	if p.Jitter < 0 || p.Jitter > 1 {
		p.Jitter = 0.2
	}
	return p
}

// Delay returns the wait before retry number attempt (1-based), jittered.
func (p Policy) Delay(attempt int) time.Duration {
	p = p.withDefaults()
	if attempt < 1 {
		attempt = 1
	}
	d := float64(p.Initial)
	for i := 1; i < attempt && d < float64(p.Max); i++ {
		d *= p.Multiplier
	}
	if d > float64(p.Max) {
		d = float64(p.Max)
	}
	if p.Jitter > 0 {
		d *= 1 + (rand.Float64()*2-1)*p.Jitter
	}
	if d < 0 {
		d = 0
	}
	return time.Duration(d)
}

// permanent wraps an error that must not be retried.
type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

// Permanent marks err as not retryable: Do returns it immediately.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanent{err}
}

// IsPermanent reports whether err was marked with Permanent.
func IsPermanent(err error) bool {
	var p permanent
	return errors.As(err, &p)
}

// Do calls fn until it succeeds, returns a Permanent error, MaxAttempts is
// reached, or ctx ends. onRetry (optional) is told about each failure and
// the upcoming delay, for logging and metrics. When ctx ends the returned
// error wraps both the context error and the last failure.
func Do(ctx context.Context, p Policy, fn func(ctx context.Context) error, onRetry func(attempt int, err error, next time.Duration)) error {
	p = p.withDefaults()
	var last error
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return wrapCtx(err, last)
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if IsPermanent(err) {
			return err
		}
		if ctx.Err() != nil {
			return wrapCtx(ctx.Err(), err)
		}
		last = err
		if p.MaxAttempts > 0 && attempt >= p.MaxAttempts {
			return fmt.Errorf("retry: gave up after %d attempts: %w", attempt, err)
		}
		next := p.Delay(attempt)
		if onRetry != nil {
			onRetry(attempt, err, next)
		}
		t := time.NewTimer(next)
		select {
		case <-ctx.Done():
			t.Stop()
			return wrapCtx(ctx.Err(), err)
		case <-t.C:
		}
	}
}

func wrapCtx(ctxErr, last error) error {
	if last == nil {
		return ctxErr
	}
	return fmt.Errorf("%w (last error: %w)", ctxErr, last)
}

// Backoff is a stateful helper for loops that manage their own waiting:
// call Next after each failure to get the delay, Reset after a success or
// after the connection has been healthy for a while.
type Backoff struct {
	Policy  Policy
	attempt int
}

// Next returns the next delay and advances the schedule.
func (b *Backoff) Next() time.Duration {
	b.attempt++
	return b.Policy.Delay(b.attempt)
}

// Reset restarts the schedule at Policy.Initial.
func (b *Backoff) Reset() { b.attempt = 0 }

// Attempt is the number of consecutive failures so far.
func (b *Backoff) Attempt() int { return b.attempt }

// Sleep waits for Next() or until ctx ends, returning ctx.Err() in that case.
func (b *Backoff) Sleep(ctx context.Context) error {
	t := time.NewTimer(b.Next())
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
