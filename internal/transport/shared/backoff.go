package shared

import (
	"context"
	"time"
)

// Default retry backoff bounds for accept and read loops. They mirror the
// net/http Server.Serve behaviour: the first retry waits DefaultBackoffMin and
// every further consecutive failure doubles the wait up to DefaultBackoffMax.
const (
	DefaultBackoffMin = 5 * time.Millisecond
	DefaultBackoffMax = time.Second
)

// Backoff rate-limits a retry loop after a non-fatal error, so a listener or
// socket that keeps failing immediately (a persistent EMFILE, a listener left
// in a bad state, a peer flooding malformed handshakes) cannot spin the loop at
// 100% CPU. Call Reset after a successful operation to clear the accumulated
// delay.
//
// Backoff holds no shared state: each loop owns its own instance, so it stays
// correct when the surrounding server is run reentrantly.
type Backoff struct {
	min   time.Duration
	max   time.Duration
	delay time.Duration
}

// NewBackoff returns a Backoff whose delay starts at min and doubles up to max.
// A non-positive min or max falls back to the package default for that bound,
// and max is never allowed below min, so a caller cannot accidentally disable
// the guard.
func NewBackoff(min, max time.Duration) *Backoff {
	if min <= 0 {
		min = DefaultBackoffMin
	}
	if max <= 0 {
		max = DefaultBackoffMax
	}
	if max < min {
		max = min
	}
	return &Backoff{min: min, max: max}
}

// Wait blocks for the next backoff delay and doubles the delay for the
// following call. It returns false when ctx is done first, which signals the
// caller to stop retrying.
func (b *Backoff) Wait(ctx context.Context) bool {
	switch {
	case b.delay <= 0:
		b.delay = b.min
	case b.delay >= b.max:
		b.delay = b.max
	default:
		if b.delay *= 2; b.delay > b.max {
			b.delay = b.max
		}
	}
	timer := time.NewTimer(b.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// Reset clears the accumulated delay after a successful operation.
func (b *Backoff) Reset() { b.delay = 0 }

// Delay reports the delay the next Wait call will use. Intended for tests.
func (b *Backoff) Delay() time.Duration { return b.delay }
