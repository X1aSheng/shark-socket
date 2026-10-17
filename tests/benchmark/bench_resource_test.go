package benchmark

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
)

// Resource-footprint benchmarks.
//
// The latency benchmarks in this package answer "how fast", which is the
// question a throughput target asks. These answer a different one: what does
// the framework hold resident while doing it. The two can move in opposite
// directions -- an implementation that avoids per-frame allocation by keeping a
// fixed-size buffer per connection trades a transient allocation for permanent
// residency, so a fast path can be the expensive one at scale.
//
// Every figure here is reported with the connection count that produced it. A
// per-connection number without N is meaningless, and a single aggregate heap
// reading hides the only slope anyone needs to extrapolate.
//
// The thresholds below are guards, not targets. They fail the benchmark when a
// change makes residency grow without a corresponding reason; they are set
// against the measured baseline of the current implementation rather than
// against a theoretical floor.

// idleConnCounts is the connection ladder for residency measurements. 10k is
// the point the figures are meant to be extrapolated from, so it is the
// top rung on hosts that can hold it.
//
// BENCH_IDLE_CONNS overrides the ladder (comma-separated), which is how a
// small host measures the same slope over a shorter rung: the per-connection
// figure comes from the delta between two rungs, so keeping at least two is
// what makes the slope observable. A single rung would report an absolute heap
// reading with the listener and harness mixed in.
var idleConnCounts = idleConnLadder()

func idleConnLadder() []int {
	raw := os.Getenv("BENCH_IDLE_CONNS")
	if raw == "" {
		return []int{100, 1000, 10000}
	}
	var counts []int
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n <= 0 {
			continue
		}
		counts = append(counts, n)
	}
	if len(counts) == 0 {
		return []int{100, 1000, 10000}
	}
	return counts
}

// resourceBudgetPerConn is the ceiling on measured resident bytes per idle
// connection.
//
// The current session holds one write-queue channel of WriteQueue slice headers
// plus the session struct and two goroutine stacks, so the expected value sits
// in the low kilobytes. A figure approaching 64 KiB per connection means the
// read path started reserving frame-sized buffers per session -- which is the
// memory amplification REARCHITECTURE-261016-v2 section 4.2 warns about, where
// a per-connection MaxFrame default of 1 MiB turns 10k connections into 10 GB.
const resourceBudgetPerConn = 64 << 10

// forceGC collapses the garbage so a post-GC HeapAlloc reflects live objects
// rather than whatever happened to be unreachable. Two calls: the first may
// promote a finalizer-bearing object, the second collects what that released.
func forceGC() {
	goruntime.GC()
	goruntime.GC()
}

// BenchmarkTCPIdleConnectionFootprint measures resident heap per idle
// connection.
//
// Idle is the interesting state: an idle connection is pure overhead, consuming
// memory while producing nothing. This is the number that decides how many
// connections fit in a given box, and it is the one a latency benchmark cannot
// produce -- an idle connection is not on any hot path.
//
// The clients here deliberately never send, so every byte measured belongs to
// the server's accept path and per-session state.
func BenchmarkTCPIdleConnectionFootprint(b *testing.B) {
	skipIfShort(b)

	for _, n := range idleConnCounts {
		b.Run(connCountName(n), func(b *testing.B) {
			h := newEchoHarness(b, func() core.Server {
				return tcp.NewServer(
					tcp.WithAddr("127.0.0.1:0"),
					tcp.WithHandler(echoHandler),
					// Long timeout so the measurement is not cut short by the
					// idle-reclaim timer: a connection closed mid-run would free
					// its residency and understate the footprint.
					tcp.WithReadTimeout(0),
				)
			})

			// Baseline first, then dial, then baseline again: the delta between
			// the two readings is the cost of n idle connections, with the
			// harness and the listener itself cancelling out.
			forceGC()
			before := snapshotMem()

			conns := dialIdle(b, h.Addr, n)
			defer func() {
				for _, c := range conns {
					_ = c.Close()
				}
			}()
			waitForSessions(b, h, n)

			forceGC()
			after := snapshotMem()

			// Goroutines are part of residency too: the read and write loops
			// each cost a stack, and a stack costs memory whether or not the
			// goroutine is scheduled.
			goroutines := after.numGoroutine

			perConn := residentPerConn(after.heapInuse, before.heapInuse, n)

			reportMemStats(b, "idle/"+connCountName(n), &before)

			b.ReportMetric(float64(perConn), "resident-B/conn")
			b.ReportMetric(float64(after.heapInuse), "heapInuse-B")
			b.ReportMetric(float64(after.totalAlloc-before.totalAlloc), "churn-B")
			b.ReportMetric(float64(goroutines)/float64(n), "goroutines/conn")
			b.ReportMetric(float64(after.numGC-before.numGC), "gc-cycles")

			if perConn > resourceBudgetPerConn {
				b.Errorf("resident heap per idle connection = %d B, budget %d B "+
					"(heap in use grew %d B across %d connections)",
					perConn, resourceBudgetPerConn,
					int64(after.heapInuse)-int64(before.heapInuse), n)
			}
		})
	}
}

// residentPerConn returns the per-connection heap delta, or 0 when the delta
// is not meaningful.
//
// A negative or tiny delta means the reading is noise, not a small footprint:
// the harness allocations and the server's own startup state sit inside the
// same measurement. Reporting a negative per-connection figure would be worse
// than reporting none, so the caller gets 0 and the guard stays silent rather
// than passing on a bogus number.
func residentPerConn(after, before uint64, conns int) int64 {
	if conns <= 0 {
		return 0
	}
	delta := int64(after) - int64(before)
	if delta <= 0 {
		return 0
	}
	return delta / int64(conns)
}

// dialIdle opens n connections that never send, then returns them.
func dialIdle(b *testing.B, addr string, n int) []net.Conn {
	b.Helper()
	conns := make([]net.Conn, 0, n)
	d := net.Dialer{Timeout: 5 * time.Second}
	for i := 0; i < n; i++ {
		c, err := d.Dial("tcp", addr)
		if err != nil {
			b.Fatalf("dial connection %d/%d: %v", i+1, n, err)
		}
		// SO_LINGER 0 makes close a reset rather than a graceful shutdown,
		// which avoids TIME_WAIT accumulation across the whole ladder.
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.SetLinger(0)
		}
		conns = append(conns, c)
	}
	return conns
}

// waitForSessions blocks until the server has registered all n connections.
//
// Dial returning only proves the client's socket connected, not that the accept
// loop registered the session. Measuring before registration would charge the
// server's session state to the "before" reading and report a footprint near
// zero -- a false pass, which is the dangerous direction for a guard.
func waitForSessions(b *testing.B, h *echoHarness, n int) {
	b.Helper()
	count := func() int64 { return h.Gateway.Runtime().Sessions().Count() }
	deadline := time.Now().Add(30 * time.Second)
	for {
		if count() >= int64(n) {
			return
		}
		if time.Now().After(deadline) {
			b.Fatalf("only %d/%d sessions registered after 30s", count(), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// BenchmarkReadLoopFrameAllocation measures the per-frame allocation on the
// read path: one frame in, one payload out.
//
// The current framer allocates a payload per frame by design, so this
// benchmark's job is to make that visible and to catch it growing. A change
// that introduces a second allocation per frame (a header slice, a framed copy)
// roughly doubles the number here, which is the regression the figure exists to
// catch. It is a tripwire for the zero-copy work in REARCHITECTURE-261016-v2
// section 3, not a claim that the path is already allocation-free.
func BenchmarkReadLoopFrameAllocation(b *testing.B) {
	for _, size := range []int{64, 1024, 4096} {
		b.Run(byteSizeName(size), func(b *testing.B) {
			framer := tcp.LengthPrefixFramer{MaxFrameBytes: 1024 * 1024}
			payload := repeatBytes(size)
			buf := frameBytes(payload)
			// One reader, reset per iteration. Allocating inside the loop
			// would charge the framer for the harness: bytes.Reader escapes to
			// the heap, adding 2 allocs/op that have nothing to do with framing
			// and would drown out the one allocation this benchmark exists to
			// count.
			reader := bytes.NewReader(nil)

			before := snapshotMem()
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				reader.Reset(buf)
				if _, err := framer.ReadFrame(reader); err != nil {
					b.Fatalf("ReadFrame: %v", err)
				}
			}
			b.StopTimer()
			reportMemStats(b, "readframe/"+byteSizeName(size), &before)
		})
	}
}

// BenchmarkSendCopy measures what copying a payload for the write queue costs.
//
// Send copies by contract, so a caller may release its buffer as soon as Send
// returns. This states what that ownership guarantee costs per frame, which is
// the number the zero-copy write path in REARCHITECTURE-261016-v2 section 3.6
// has to be weighed against: dropping the copy also drops the guarantee.
func BenchmarkSendCopy(b *testing.B) {
	for _, size := range []int{64, 1024, 4096} {
		b.Run(byteSizeName(size), func(b *testing.B) {
			payload := repeatBytes(size)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf := make([]byte, size)
				copy(buf, payload)
				_ = buf
			}
		})
	}
}

// BenchmarkFrameSplitAcrossSegments exercises the partial-read path: a frame
// whose bytes arrive spread over many small reads, which is what a slow or
// fragmented network does to every long-lived connection.
//
// io.ReadFull already loops until the frame is whole, so correctness here is
// inherited rather than newly implemented. What the benchmark measures is the
// cost of that loop, and more usefully that a fragmented read does not silently
// truncate: a short frame is the failure mode that yields corrupted protocol
// state instead of a clean error, so the equality check is the point.
func BenchmarkFrameSplitAcrossSegments(b *testing.B) {
	payload := repeatBytes(512)
	buf := frameBytes(payload)

	for _, segment := range []int{1, 7, 64} {
		b.Run(segmentName(segment), func(b *testing.B) {
			framer := tcp.LengthPrefixFramer{MaxFrameBytes: 1024 * 1024}

			// Verified once outside the timed loop. Asserting per iteration
			// would fold bytes.Equal's cost into the number.
			if got, err := framer.ReadFrame(&segmentedReader{data: buf, segment: segment}); err != nil {
				b.Fatalf("fragmented read: %v", err)
			} else if !bytes.Equal(got, payload) {
				b.Fatalf("truncated frame: got %d bytes, want %d", len(got), len(payload))
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := &segmentedReader{data: buf, segment: segment}
				got, err := framer.ReadFrame(r)
				if err != nil {
					b.Fatalf("fragmented read: %v", err)
				}
				if len(got) != len(payload) {
					b.Fatalf("truncated frame: got %d bytes, want %d", len(got), len(payload))
				}
			}
		})
	}
}

// segmentedReader hands out at most segment bytes per Read, so one frame
// arrives as many partial reads.
type segmentedReader struct {
	data    []byte
	segment int
	offset  int
}

func (r *segmentedReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := r.segment
	if n > len(p) {
		n = len(p)
	}
	if remaining := len(r.data) - r.offset; n > remaining {
		n = remaining
	}
	copy(p, r.data[r.offset:r.offset+n])
	r.offset += n
	return n, nil
}

// repeatBytes builds a size-byte payload with a non-uniform pattern, so a
// truncated or reordered frame is distinguishable from one that merely has the
// right length.
func repeatBytes(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte(i*31 + i/7)
	}
	return out
}

// frameBytes wraps payload in the length prefix the TCP framer expects.
func frameBytes(payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(out[:4], uint32(len(payload)))
	copy(out[4:], payload)
	return out
}

func segmentName(n int) string {
	return strconv.Itoa(n) + "B-segments"
}

// ---------------------------------------------------------------------------
// MemStats snapshot (REARCHITECTURE-261016-v2 section 10.3, item 4)
// ---------------------------------------------------------------------------

// memSnapshot is the process-wide memory state at one instant.
//
// Taken from the benchmark process rather than the runner: `go test` forks a
// separate binary, so the runner's own counters say nothing about what the
// framework did. REARCHITECTURE-261016-v2 section 10.3 concludes that GC
// behaviour must be measurable rather than guessed, and this is the measurement.
type memSnapshot struct {
	heapAlloc    uint64
	heapInuse    uint64
	heapSys      uint64
	heapReleased uint64
	totalAlloc   uint64
	mallocs      uint64
	numGC        uint32
	pauseTotalNs uint64
	stackInuse   uint64
	numGoroutine int
}

// snapshotMem records the counters that distinguish the three things that are
// easy to conflate: memory held (HeapInuse), memory ever requested (TotalAlloc),
// and how often the collector ran (NumGC).
//
// A run can show flat HeapInuse and a rising TotalAlloc -- steady residency with
// heavy churn. Reporting only the heap makes that look like a clean result, so
// both are captured.
func snapshotMem() memSnapshot {
	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)
	return memSnapshot{
		heapAlloc:    ms.HeapAlloc,
		heapInuse:    ms.HeapInuse,
		heapSys:      ms.HeapSys,
		heapReleased: ms.HeapReleased,
		totalAlloc:   ms.TotalAlloc,
		mallocs:      ms.Mallocs,
		numGC:        ms.NumGC,
		pauseTotalNs: ms.PauseTotalNs,
		stackInuse:   ms.StackInuse,
		numGoroutine: goruntime.NumGoroutine(),
	}
}

// String renders one snapshot as a single line of counters, without the
// MEMSTATS marker -- reportMemStats adds that, along with the label.
func (s memSnapshot) String() string {
	return fmt.Sprintf("heapAlloc=%dB heapInuse=%dB heapSys=%dB heapReleased=%dB "+
		"totalAlloc=%dB mallocs=%d numGC=%d pauseTotalNs=%d stackInuse=%dB goroutines=%d",
		s.heapAlloc, s.heapInuse, s.heapSys, s.heapReleased,
		s.totalAlloc, s.mallocs, s.numGC, s.pauseTotalNs, s.stackInuse, s.numGoroutine)
}

// reportMemStats logs a labelled snapshot and the delta from an optional
// earlier one, so a benchmark can attribute movement across its own body.
//
// Called from benchmarks rather than TestMain because the interesting delta is
// per-benchmark: the process-wide figure after the whole suite reflects Go's
// own test infrastructure at least as much as the framework under test.
func reportMemStats(b *testing.B, label string, before *memSnapshot) {
	after := snapshotMem()
	b.Logf("MEMSTATS %s %s", label, after)
	if before != nil {
		b.Logf("MEMSTATS %s delta heapAlloc=%+dB heapInuse=%+dB totalAlloc=%+dB mallocs=%+d "+
			"numGC=%+d pauseTotalNs=%+dns goroutines=%+d",
			label,
			int64(after.heapAlloc)-int64(before.heapAlloc),
			int64(after.heapInuse)-int64(before.heapInuse),
			after.totalAlloc-before.totalAlloc,
			after.mallocs-before.mallocs,
			after.numGC-before.numGC,
			after.pauseTotalNs-before.pauseTotalNs,
			after.numGoroutine-before.numGoroutine)
	}
}
