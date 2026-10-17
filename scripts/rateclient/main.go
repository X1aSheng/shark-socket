// Package main provides a rate-limited load client with latency percentiles.
//
// Why a rate-limited client exists: the flood-style client used for
// saturation runs unthrottled, so on a single host it becomes the bottleneck
// and steals CPU from the server under test. That makes the measurement
// meaningless. This client paces sends with a ticker so the observed
// throughput reflects the server's ceiling rather than the client's greed.
//
// Why percentiles and a time series: totals alone cannot distinguish a server
// that sustains its rate from one that starts fast and collapses. A run can
// report an identical sent/recv total while being saturated for the last 90%
// of its window. The p50/p90/p99/p999 spread says whether the tail is fat, and
// the per-second series says when the degradation started.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// maxLatencySamples caps how many echo round-trips are kept for the
// percentile computation.
//
// The sampler records at most one sample per batch, so the natural bound is
// rate/batch per second, which is small. But a misconfigured rate with batch=1
// makes that bound equal the message rate, and a fast link can produce millions
// of samples in one window: at 8 bytes each, a few million samples is tens of
// megabytes on the load generator -- memory this client is stealing from the
// server it is trying to measure.
//
// Once the cap is hit, sampling stops and every later echo reports only through
// the counters. That trades tail resolution for a generator that cannot become
// the bottleneck, which is the right way round.
const maxLatencySamples = 200_000

func main() {
	host := flag.String("host", "127.0.0.1:28000", "server address host:port")
	conns := flag.Int("conns", 1, "number of concurrent connections")
	rate := flag.Int("rate", 2000, "target messages per second across all connections")
	dur := flag.Duration("duration", 8*time.Second, "test duration")
	size := flag.Int("size", 256, "payload size in bytes")
	label := flag.String("label", "", "label echoed in the output, e.g. the offered rate")
	flag.Parse()

	var sent, recv, errs atomic.Int64

	// The server's default framer is LengthPrefixFramer, so every write needs a
	// 4-byte big-endian length header. Writing bare payload bytes does not
	// "work but counts less": the server reads the first four payload bytes as a
	// length, which for a zeroed buffer means a zero-length frame, so the run
	// reports healthy numbers while measuring nothing. Encode once and reuse.
	frame := make([]byte, 4+*size)
	binary.BigEndian.PutUint32(frame[:4], uint32(*size))

	perConnRate := *rate / *conns
	if perConnRate < 1 {
		perConnRate = 1
	}
	// Batch the ticker: a ticker whose period is a few microseconds cannot
	// keep up and silently caps the achieved rate. Send `batch`
	// messages per tick so that rate/conns/batch stays above the ticker's
	// practical floor (~1ms).
	batch := perConnRate / 20
	if batch < 1 {
		batch = 1
	}
	effective := perConnRate / batch
	if effective < 1 {
		effective = 1
	}
	interval := time.Second / time.Duration(effective)
	deadline := time.Now().Add(*dur)

	// Echo replies are counted on a shared atomic; the read loop must outlive
	// the send loop, so it is joined separately instead of racing it.
	var readers sync.WaitGroup
	var writers sync.WaitGroup
	// readStop bounds the reader loops: a ReadDeadline timeout is their normal
	// wake-up, so without a deadline-based exit condition they never finish.
	readStop := time.Now().Add(*dur + 500*time.Millisecond)

	// Latency samples are per-echo: measured from when a frame was written to
	// when its reply was read. Only connections with an in-flight frame can
	// produce one, so the sample count is bounded by the number of frames
	// written, not by the number of frames received.
	states := make([]*connState, *conns)

	// sampled counts every echo seen, including those past the retention cap,
	// so the summary can report how many were dropped rather than silently
	// presenting a truncated distribution as a complete one.
	var sampled atomic.Int64

	for i := 0; i < *conns; i++ {
		st := &connState{}
		states[i] = st
		conn, err := net.Dial("tcp", *host)
		if err != nil {
			errs.Add(1)
			continue
		}
		readers.Add(1)
		go func(c net.Conn, st *connState) {
			defer readers.Done()
			defer c.Close()
			buf := make([]byte, 64*1024)
			// Read in whole frames: a reply carries its own length header, so
			// counting raw Read() bytes would inflate the message count by
			// however much the stream happened to coalesce.
			var hdr [4]byte
			for time.Now().Before(readStop) {
				_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
				if _, err := readFull(c, hdr[:]); err != nil {
					// A timeout is the normal wake-up: the reader polls until
					// readStop passes. Any other error ends the loop.
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						continue
					}
					return
				}
				n := binary.BigEndian.Uint32(hdr[:])
				if n > uint32(len(buf)) {
					return
				}
				if _, err := readFull(c, buf[:n]); err != nil {
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						continue
					}
					return
				}
				recv.Add(1)
				recordEcho(st, &sampled)
			}
		}(conn, st)

		writers.Add(1)
		go func(c net.Conn, st *connState) {
			defer writers.Done()
			tk := time.NewTicker(interval)
			defer tk.Stop()
			for range tk.C {
				if time.Now().After(deadline) {
					return
				}
				for range batch {
					markSent(st)
					if _, err := c.Write(frame); err != nil {
						errs.Add(1)
						return
					}
					sent.Add(1)
				}
			}
		}(conn, st)
	}

	// Sample the per-second counters while the load runs. Collected by a
	// separate goroutine rather than by polling inside the send loops so the
	// sampling cadence does not perturb the send cadence.
	//
	// The loop is bounded by tick count, not by a wall-clock comparison. An
	// earlier version compared time.Now() against a deadline and appended on
	// every pass; once the deadline passed, the computed next-tick instant was
	// already in the past, the sleep was zero, and the loop spun appending one
	// sample per iteration until the process was OOM-killed. A fixed tick budget
	// makes the series length independent of how fast the loop iterates.
	maxTicks := int(dur.Seconds()) + 3
	series := make([]secondSample, 0, maxTicks)
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		start := time.Now()
		prevSent, prevRecv := int64(0), int64(0)
		for tick := 1; tick <= maxTicks; tick++ {
			if wait := time.Until(start.Add(time.Duration(tick) * time.Second)); wait > 0 {
				time.Sleep(wait)
			}
			curSent, curRecv := sent.Load(), recv.Load()
			series = append(series, secondSample{
				second: tick,
				sent:   curSent - prevSent,
				recv:   curRecv - prevRecv,
			})
			prevSent, prevRecv = curSent, curRecv
		}
	}()

	writers.Wait()
	// Give in-flight echoes a moment to arrive before tearing readers down.
	time.Sleep(300 * time.Millisecond)
	readers.Wait()
	sampler.Wait()

	all := make([]time.Duration, 0, min(int64(maxLatencySamples), recv.Load()))
	for _, st := range states {
		all = append(all, st.snapshot()...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })

	fmt.Printf("label=%-12s conns=%-4d target=%-8d size=%-5d sent=%-10d recv=%-10d errs=%-4d send_rate=%.0f/s\n",
		orDash(*label), *conns, *rate, *size, sent.Load(), recv.Load(), errs.Load(),
		float64(sent.Load())/dur.Seconds())

	// The achieved-versus-offered ratio is the load generator's own honesty
	// check: a low ratio means the client could not reach the target and the
	// "server limit" being observed is the client's, not the server's.
	if *rate > 0 {
		fmt.Printf("achieved_ratio=%.3f (offered %.0f/s, achieved %.0f/s)\n",
			float64(recv.Load())/dur.Seconds()/float64(*rate),
			float64(*rate), float64(recv.Load())/dur.Seconds())
	}

	if len(all) > 0 {
		fmt.Printf("latency_samples=%d p50=%s p90=%s p99=%s p999=%s max=%s\n",
			len(all),
			percentile(all, 0.50).Round(time.Microsecond),
			percentile(all, 0.90).Round(time.Microsecond),
			percentile(all, 0.99).Round(time.Microsecond),
			percentile(all, 0.999).Round(time.Microsecond),
			all[len(all)-1].Round(time.Microsecond))
		// The p99/p50 ratio is the tail-fatness indicator. A healthy run keeps
		// it near 1; queueing collapses push it well above 10.
		p50, p99 := percentile(all, 0.50), percentile(all, 0.99)
		if p50 > 0 {
			fmt.Printf("tail_ratio_p99_p50=%.2f\n", float64(p99)/float64(p50))
		}
		// Say so when the sample cap was reached: the percentiles then describe
		// the first part of the run only, and reading them as the whole window
		// would hide a late collapse.
		if sampled.Load() > maxLatencySamples {
			fmt.Printf("latency_samples_capped=true (cap=%d, %d echoes seen; percentiles cover the earlier part of the run)\n",
				maxLatencySamples, sampled.Load())
		}
	} else {
		fmt.Printf("latency_samples=0 (no echo completed; server unreachable or refused every connection)\n")
	}

	fmt.Println("per_second (second sent recv):")
	for _, s := range series {
		fmt.Printf("  %3d %10d %10d\n", s.second, s.sent, s.recv)
	}
}

// connState holds one connection's latency samples.
//
// Each connection samples into its own slice and merges at the end. A single
// shared slice would need a lock on the read path, which on a saturated server
// is precisely the path being measured.
type connState struct {
	mu       sync.Mutex
	samples  []time.Duration
	inflight time.Time
	hasOne   bool
}

// secondSample is one second of the counter deltas.
type secondSample struct {
	second int
	sent   int64
	recv   int64
}

// markSent records when this connection's next frame goes out, so the echo can
// be timed against it.
//
// With batch > 1 only the first frame of a batch is timed. Frames 2..batch are
// written back to back into a socket buffer and their replies come back in the
// same read, so attributing a send time to each of them would charge the first
// one's write time to the rest and inflate the reported latency.
func markSent(st *connState) {
	st.mu.Lock()
	if !st.hasOne {
		st.inflight = time.Now()
		st.hasOne = true
	}
	st.mu.Unlock()
}

// recordEcho banks the round-trip time for the frame that markSent stamped.
func recordEcho(st *connState, sampled *atomic.Int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.hasOne {
		return
	}
	st.hasOne = false
	if sampled.Add(1) > maxLatencySamples {
		return
	}
	st.samples = append(st.samples, time.Since(st.inflight))
}

func (s *connState) snapshot() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]time.Duration, len(s.samples))
	copy(out, s.samples)
	return out
}

// percentile returns the p-th percentile of a sorted slice using the
// nearest-rank method. p is clamped so an out-of-range call cannot panic on an
// empty slice: callers pass fixed values, but a load client that dies on its
// final summary line has already lost the run.
func percentile(sorted []time.Duration, p float64) time.Duration {
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

// readFull reads exactly len(buf) bytes, so a frame split across TCP segments
// is reassembled rather than mis-counted.
func readFull(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
