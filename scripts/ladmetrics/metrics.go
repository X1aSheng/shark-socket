// Package ladmetrics holds the measurement layer shared by the load
// generators under scripts/.
//
// It exists so that a second generator does not reimplement percentile maths and
// the per-second series. The first one (rateclient) grew those in place, and by
// the time a multi-protocol generator was needed there was already a subtle bug
// in the sampling loop that took several rounds to localise; having the logic
// in one place means the next protocol inherits the fix rather than repeating
// the mistake.
//
// Nothing here knows about a protocol. A generator reports sends, receives and
// round-trip samples, and this package turns them into the summary line.
package ladmetrics

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// MaxSamples caps how many round-trip samples are kept for the percentile
// computation.
//
// The natural bound is rate/batch per second, which is small. But a
// misconfigured rate with batch=1 makes that bound equal the message rate, and a
// fast link can produce millions of samples in one window: at 8 bytes each that
// is tens of megabytes on the load generator -- memory it is stealing from the
// server it is trying to measure.
//
// Once the cap is hit, sampling stops and later messages report only through
// the counters. That trades tail resolution for a generator that cannot become
// the bottleneck, which is the right way round.
const MaxSamples = 200_000

// Conn tracks one connection's in-flight frame and its latency samples.
//
// Per-connection rather than shared on purpose: a shared slice would need a
// lock on the read path, and on a saturated server the read path is the very
// thing being measured.
type Conn struct {
	mu       sync.Mutex
	samples  []time.Duration
	inflight time.Time
	hasOne   bool
}

// Second is one second of counter deltas.
type Second struct {
	Second int
	Sent   int64
	Recv   int64
}

// MarkSent stamps when this connection's next frame goes out.
//
// With batching, only the first frame of a batch is timed: the rest are written
// back to back into a socket buffer and their replies come back in one read, so
// stamping each of them would charge the first frame's write time to the rest.
func (c *Conn) MarkSent() {
	c.mu.Lock()
	if !c.hasOne {
		c.inflight = time.Now()
		c.hasOne = true
	}
	c.mu.Unlock()
}

// RecordEcho banks the round-trip time for the frame MarkSent stamped.
func (c *Conn) RecordEcho(sampled *atomic.Int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasOne {
		return
	}
	c.hasOne = false
	if sampled.Add(1) > MaxSamples {
		return
	}
	c.samples = append(c.samples, time.Since(c.inflight))
}

// Snapshot copies the retained samples for merging at the end of a run.
func (c *Conn) Snapshot() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.samples))
	copy(out, c.samples)
	return out
}

// Percentile returns the p-th percentile of a sorted slice by nearest rank.
// p is clamped so an out-of-range call cannot panic on an empty slice: a load
// client that dies on its final summary line has already lost the run.
func Percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Collector accumulates a run's counters and the per-second series.
type Collector struct {
	Sent   atomic.Int64
	Recv   atomic.Int64
	Errs   atomic.Int64
	sample atomic.Int64

	mu    sync.Mutex
	conns []*Conn
	// series is a plain slice, not a channel: a bounded channel deadlocks when
	// the consumer only drains after the producer has stopped, which is exactly
	// the ordering a run uses.
	series []Second
}

// NewCollector returns a Collector tracking n connections.
func NewCollector(n int) *Collector {
	c := &Collector{conns: make([]*Conn, n)}
	// Allocate each tracker. make([]*Conn, n) only fills nil pointers, and a
	// nil *Conn panics the moment a summary is taken.
	for i := range c.conns {
		c.conns[i] = &Conn{}
	}
	return c
}

// Conn returns the i-th connection tracker.
func (c *Collector) Conn(i int) *Conn { return c.conns[i] }

// RecordEcho banks a round-trip against connection i and counts the receive.
func (c *Collector) RecordEcho(i int) {
	c.Recv.Add(1)
	c.conns[i].RecordEcho(&c.sample)
}

// NoteSend counts one successful send.
func (c *Collector) NoteSend() { c.Sent.Add(1) }

// NoteError counts one failed exchange.
func (c *Collector) NoteError() { c.Errs.Add(1) }

// Series returns the per-second counter series collected so far.
func (c *Collector) Series() []Second {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Second, len(c.series))
	copy(out, c.series)
	return out
}

// Collect samples the counters once per second for the given number of ticks.
//
// The loop is bounded by tick count, not by a wall-clock comparison. An earlier
// version compared time.Now() against a deadline and appended every pass; once
// the deadline passed, the computed next-tick instant was already in the past,
// the sleep was zero, and the loop spun appending until the process was
// OOM-killed. A fixed tick budget makes the series length independent of how fast
// the loop iterates.
//
// Call it from its own goroutine so the sampling cadence does not perturb the
// send cadence.
func (c *Collector) Collect(ticks int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	start := time.Now()
	prevSent, prevRecv := int64(0), int64(0)
	c.series = make([]Second, 0, ticks)
	for tick := 1; tick <= ticks; tick++ {
		if wait := time.Until(start.Add(time.Duration(tick) * time.Second)); wait > 0 {
			time.Sleep(wait)
		}
		sent, recv := c.Sent.Load(), c.Recv.Load()
		c.series = append(c.series, Second{
			Second: tick,
			Sent:   sent - prevSent,
			Recv:   recv - prevRecv,
		})
		prevSent, prevRecv = sent, recv
	}
}

// Report is the summary of a finished run.
type Report struct {
	Label       string
	Protocol    string
	Conns       int
	Size        int
	Duration    time.Duration
	Sent        int64
	Recv        int64
	Errs        int64
	Achieved    float64
	Offered     float64
	Samples     int
	TotalSampl  int64
	Percentiles [5]time.Duration // p50, p90, p99, p999, max
	TailRatio   float64
	Capped      bool
	Series      []Second
}

// Summarize merges every connection's samples and computes the summary.
//
// offered is the target rate in messages per second; achieved is derived from
// the receive count over the run duration.
func (c *Collector) Summarize(label, protocol string, conns, size int, offered float64, dur time.Duration) Report {
	seen := c.sample.Load()
	all := make([]time.Duration, 0, min(int64(MaxSamples), seen))
	for _, conn := range c.conns {
		all = append(all, conn.Snapshot()...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })

	recv := c.Recv.Load()
	rep := Report{
		Label:      label,
		Protocol:   protocol,
		Conns:      conns,
		Size:       size,
		Duration:   dur,
		Sent:       c.Sent.Load(),
		Recv:       recv,
		Errs:       c.Errs.Load(),
		Offered:    offered,
		Samples:    len(all),
		TotalSampl: seen,
		Capped:     seen > MaxSamples,
		Series:     c.Series(),
	}
	if dur > 0 {
		rep.Achieved = float64(recv) / dur.Seconds()
	}
	if len(all) > 0 {
		rep.Percentiles = [5]time.Duration{
			Percentile(all, 0.50),
			Percentile(all, 0.90),
			Percentile(all, 0.99),
			Percentile(all, 0.999),
			all[len(all)-1],
		}
		if p50 := rep.Percentiles[0]; p50 > 0 {
			rep.TailRatio = float64(rep.Percentiles[2]) / float64(p50)
		}
	}
	return rep
}

// String renders the report as the block a run prints on completion.
func (r Report) String() string {
	var b []byte
	b = append(b, fmt.Sprintf("label=%s protocol=%s conns=%d size=%d sent=%d recv=%d errs=%d\n",
		orDash(r.Label), r.Protocol, r.Conns, r.Size, r.Sent, r.Recv, r.Errs)...)
	if r.Offered > 0 {
		b = append(b, fmt.Sprintf("achieved_ratio=%.3f (offered %.0f/s, achieved %.0f/s)\n",
			r.Achieved/r.Offered, r.Offered, r.Achieved)...)
	}
	if r.Samples > 0 {
		b = append(b, fmt.Sprintf("latency_samples=%d p50=%s p90=%s p99=%s p999=%s max=%s\n",
			r.Samples,
			r.Percentiles[0].Round(time.Microsecond),
			r.Percentiles[1].Round(time.Microsecond),
			r.Percentiles[2].Round(time.Microsecond),
			r.Percentiles[3].Round(time.Microsecond),
			r.Percentiles[4].Round(time.Microsecond))...)
		if r.TailRatio > 0 {
			// The p99/p50 ratio is the tail-fatness indicator. A healthy run
			// keeps it near 1; queueing collapses push it well above 10.
			b = append(b, fmt.Sprintf("tail_ratio_p99_p50=%.2f\n", r.TailRatio)...)
		}
		if r.Capped {
			// Say so when the cap was hit: the percentiles then describe the
			// earlier part of the run only, and reading them as the whole window
			// would hide a late collapse.
			b = append(b, fmt.Sprintf(
				"latency_samples_capped=true (cap=%d, %d echoes seen; percentiles cover the earlier part)\n",
				MaxSamples, r.TotalSampl)...)
		}
	}
	if len(r.Series) > 0 {
		b = append(b, "per_second (second sent recv):\n"...)
		for _, s := range r.Series {
			b = append(b, fmt.Sprintf("    %-4d %8d %8d\n", s.Second, s.Sent, s.Recv)...)
		}
	}
	return string(b)
}

// OrDash renders an empty string as a placeholder so a summary line never has
// an empty field.
func OrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orDash(s string) string { return OrDash(s) }
