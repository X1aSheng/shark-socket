package main

import (
	"math"
	"strings"
	"testing"
)

const sampleBenchOutput = `goos: linux
goarch: amd64
pkg: github.com/X1aSheng/shark-socket/tests/benchmark
cpu: AMD EPYC 7763 64-Core Processor
BenchmarkPluginChain_5Plugins-16   	 2000000	       520.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkPluginChain_5Plugins-16   	 2000000	       512.3 ns/op	       0 B/op	       0 allocs/op
BenchmarkPluginChain_5Plugins-16   	 2000000	       505.9 ns/op	       0 B/op	       0 allocs/op
BenchmarkSendCopy-16               	  500000	      2100.0 ns/op	    1024 B/op	       1 allocs/op
BenchmarkThroughput-16             	    1000	      1000.0 ns/op	      25.00 MB/s	      16 B/op	       2 allocs/op
PASS
ok  	github.com/X1aSheng/shark-socket/tests/benchmark	3.2s
`

func TestParseBenchOutput(t *testing.T) {
	runs := parseBenchOutput(sampleBenchOutput)
	if len(runs) != 3 {
		t.Fatalf("parsed %d benchmarks, want 3: %v", len(runs), keysOf(runs))
	}

	chain, ok := runs["BenchmarkPluginChain_5Plugins"]
	if !ok {
		t.Fatalf("missing BenchmarkPluginChain_5Plugins in %v", keysOf(runs))
	}
	if got := chain.median().nsPerOp; got != 512.3 {
		t.Errorf("ns/op = %v, want the median 512.3", got)
	}
	if chain.count() != 3 {
		t.Errorf("samples = %d, want 3", chain.count())
	}
	if got := chain.median().allocsPerOp; got != 0 {
		t.Errorf("allocs/op = %v, want 0", got)
	}
	// Measurement order has to survive: the paired ratio pairs sample i with
	// sample i, so sorting here would silently misalign the two sides.
	if chain.samples[0].nsPerOp != 520.1 || chain.samples[2].nsPerOp != 505.9 {
		t.Errorf("samples were reordered: %v", chain.samples)
	}

	copyRun, ok := runs["BenchmarkSendCopy"]
	if !ok {
		t.Fatalf("missing BenchmarkSendCopy in %v", keysOf(runs))
	}
	// The iteration-count column has no unit; the pairs after it must still
	// line up or B/op and allocs/op are lost.
	med := copyRun.median()
	if med.bytesPerOp != 1024 || med.allocsPerOp != 1 {
		t.Errorf("B/op, allocs/op = %v, %v want 1024, 1", med.bytesPerOp, med.allocsPerOp)
	}

	throughput, ok := runs["BenchmarkThroughput"]
	if !ok {
		t.Fatalf("missing BenchmarkThroughput in %v", keysOf(runs))
	}
	// An extra metric column (MB/s) must not shift the ones after it.
	med = throughput.median()
	if med.bytesPerOp != 16 || med.allocsPerOp != 2 {
		t.Errorf("B/op, allocs/op = %v, %v want 16, 2", med.bytesPerOp, med.allocsPerOp)
	}
}

func TestStripProcs(t *testing.T) {
	cases := map[string]string{
		"BenchmarkEcho-16":    "BenchmarkEcho",
		"BenchmarkEcho":       "BenchmarkEcho",
		"BenchmarkA-b-8":      "BenchmarkA-b",
		"BenchmarkEcho-16/x2": "BenchmarkEcho-16/x2", // sub-benchmark, not GOMAXPROCS
	}
	for in, want := range cases {
		if got := stripProcs(in); got != want {
			t.Errorf("stripProcs(%q) = %q, want %q", in, got, want)
		}
	}
}

func samplesOf(ns ...float64) []sample {
	out := make([]sample, len(ns))
	for i, v := range ns {
		out[i] = sample{nsPerOp: v}
	}
	return out
}

func TestPairedRatioIsMeanOfRatios(t *testing.T) {
	p := pairedRatioOf(samplesOf(100, 100, 100, 100, 100), samplesOf(110, 110, 110, 110, 110))
	if p.n != 5 {
		t.Errorf("n = %d, want 5", p.n)
	}
	if math.Abs(p.mean-1.1) > 1e-9 {
		t.Errorf("mean = %v, want 1.1", p.mean)
	}
	// Identical ratios have no spread, so the bound collapses onto the mean.
	if p.half != 0 {
		t.Errorf("half width = %v, want 0", p.half)
	}

	// A ratio that swings must widen the bound rather than move the estimate
	// much: that is the whole point of judging on lower().
	noisy := pairedRatioOf(samplesOf(100, 100, 100), samplesOf(110, 400, 110))
	if math.Abs(noisy.mean-(110.0/100+400.0/100+110.0/100)/3) > 1e-9 {
		t.Errorf("mean = %v, want the mean of the three ratios", noisy.mean)
	}
	if noisy.half < 0.5 {
		t.Errorf("half width = %v, want the outlier to widen the bound", noisy.half)
	}
	if noisy.fails(1.20) {
		t.Errorf("a ratio that swings between 1.1x and 4x must not fail a 20%% limit")
	}
}

func TestPairedRatioHandlesUnequalCounts(t *testing.T) {
	// A benchmark that appears or disappears partway through leaves the two
	// sides with different counts; the extra samples are ignored rather than
	// producing an out-of-range pair.
	p := pairedRatioOf(samplesOf(100, 100), samplesOf(120, 120, 120, 120))
	if math.Abs(p.mean-1.2) > 1e-9 || p.n != 2 {
		t.Errorf("mean, n = %v, %v want 1.2, 2", p.mean, p.n)
	}
	if p := pairedRatioOf(nil, samplesOf(100)); p.mean != 1 || p.n != 0 {
		t.Errorf("mean, n with no base samples = %v, %v want 1, 0", p.mean, p.n)
	}
	// A zero ns/op would divide by zero; the comparison becomes a no-op.
	if p := pairedRatioOf(samplesOf(0, 0), samplesOf(10, 10)); p.mean != 1 {
		t.Errorf("mean with a zero base = %v, want 1", p.mean)
	}
}

// TestFailsNeedsEnoughSamples pins the floor: two samples cannot support a
// standard error, so the gate reports them and declines to judge.
func TestFailsNeedsEnoughSamples(t *testing.T) {
	p := pairedRatioOf(samplesOf(100, 100), samplesOf(500, 500))
	if p.fails(1.20) {
		t.Error("2x slower must still not fail with only two samples")
	}
	p = pairedRatioOf(samplesOf(100, 100, 100), samplesOf(500, 500, 500))
	if !p.fails(1.20) {
		t.Error("2x slower with three identical samples must fail")
	}
}

// TestFailsUsesTheLowerBound is the rule that ended the false positives: an
// estimate above the limit is not enough on its own.
func TestFailsUsesTheLowerBound(t *testing.T) {
	// Mean 1.4x slower, but the samples range from 0.6x to 3x, which is a
	// machine that cannot resolve anything. Lower bound lands below the limit.
	p := pairedRatioOf(samplesOf(100, 100, 100, 100, 100, 100), samplesOf(60, 300, 100, 200, 120, 60))
	if p.mean < 1.20 {
		t.Fatalf("mean = %v, the case is meant to be above the limit", p.mean)
	}
	if p.fails(1.20) {
		t.Errorf("lower bound %v should not clear the limit", p.lower())
	}

	// The same 40% with tight samples is a real regression.
	p = pairedRatioOf(samplesOf(100, 101, 99, 100, 100, 100), samplesOf(140, 141, 139, 140, 140, 140))
	if !p.fails(1.20) {
		t.Errorf("a consistent 40%% slowdown must fail, lower bound %v", p.lower())
	}
}

// TestTCriticalIsConservativeAtSmallN guards the multiplier table: getting df
// off by one would quietly make the gate more confident than it should be.
func TestTCriticalIsConservativeAtSmallN(t *testing.T) {
	if got := tCritical(2); got != 2.920 {
		t.Errorf("tCritical(2) = %v, want 2.920", got)
	}
	if got := tCritical(5); got != 2.015 {
		t.Errorf("tCritical(5) = %v, want 2.015", got)
	}
	if got := tCritical(1); got != 6.314 {
		t.Errorf("tCritical(1) = %v, want 6.314", got)
	}
	if got := tCritical(0); got != 6.314 {
		t.Errorf("tCritical(0) = %v, want the df=1 value rather than a panic", got)
	}
	if got := tCritical(500); got != 1.645 {
		t.Errorf("tCritical(500) = %v, want the normal limit 1.645", got)
	}
	for df := 1; df < 30; df++ {
		if tCritical(df) <= tCritical(df+1) {
			t.Fatalf("table is not decreasing at df=%d", df)
		}
	}
}

func runOf(name string, ns, allocs float64) *run {
	s := sample{nsPerOp: ns, allocsPerOp: allocs}
	return &run{name: name, samples: []sample{s, s, s}}
}

// runWith builds a run whose samples vary per iteration.
func runWith(name string, samples ...sample) *run {
	return &run{name: name, samples: samples}
}

func runsOf(r *run) map[string]*run {
	return map[string]*run{r.name: r}
}

func TestComparePassesWithinThreshold(t *testing.T) {
	c := compare(runsOf(runOf("A", 100, 1)), runsOf(runOf("A", 110, 1)), 1.20, nil)
	if c.failed() {
		t.Fatalf("10%% slower than a 20%% allowance should pass, got %v", c.failures())
	}
}

func TestCompareFlagsSlowerBenchmark(t *testing.T) {
	c := compare(runsOf(runOf("A", 100, 1)), runsOf(runOf("A", 130, 1)), 1.20, nil)
	failures := c.failures()
	if len(failures) != 1 || !strings.Contains(failures[0], "slower") {
		t.Fatalf("failures = %v, want one ns/op regression", failures)
	}
}

// TestCompareFlagsAllocationIncrease pins the rule that is not a ratio: one
// extra allocation per op is a real change, no matter how much faster the
// benchmark got.
func TestCompareFlagsAllocationIncrease(t *testing.T) {
	c := compare(runsOf(runOf("A", 100, 1)), runsOf(runOf("A", 80, 2)), 1.20, nil)
	failures := c.failures()
	if len(failures) != 1 || !strings.Contains(failures[0], "allocs/op") {
		t.Fatalf("failures = %v, want one allocation regression", failures)
	}
}

// TestCompareAllocationUsesMedian: an allocation that only happens on some
// iterations is still a regression, and an ns/op win must not excuse it.
func TestCompareAllocationUsesMedian(t *testing.T) {
	base := runWith("A",
		sample{nsPerOp: 100, allocsPerOp: 0},
		sample{nsPerOp: 100, allocsPerOp: 0},
		sample{nsPerOp: 100, allocsPerOp: 0},
	)
	head := runWith("A",
		sample{nsPerOp: 99, allocsPerOp: 0},
		sample{nsPerOp: 99, allocsPerOp: 1},
		sample{nsPerOp: 99, allocsPerOp: 1},
	)
	c := compare(runsOf(base), runsOf(head), 1.20, nil)
	failures := c.failures()
	if len(failures) != 1 || !strings.Contains(failures[0], "allocs/op") {
		t.Fatalf("failures = %v, want one allocation regression", failures)
	}
}

func TestCompareHonoursExemption(t *testing.T) {
	base := runsOf(runOf("Noisy", 100, 1))
	head := runsOf(runOf("Noisy", 200, 1))
	if !compare(base, head, 1.20, nil).failed() {
		t.Fatal("2x slower should fail without an exemption")
	}
	if compare(base, head, 1.20, map[string]bool{"Noisy": true}).failed() {
		t.Fatal("exempt benchmark should not fail")
	}
}

func TestCompareTracksUnmatchedBenchmarks(t *testing.T) {
	base := runsOf(runOf("Gone", 100, 1))
	base["Both"] = runOf("Both", 100, 0)
	head := runsOf(runOf("New", 100, 1))
	head["Both"] = runOf("Both", 100, 0)

	c := compare(base, head, 1.20, nil)
	if len(c.onlyHead) != 1 || c.onlyHead[0] != "New" {
		t.Errorf("onlyHead = %v, want [New]", c.onlyHead)
	}
	if len(c.onlyBase) != 1 || c.onlyBase[0] != "Gone" {
		t.Errorf("onlyBase = %v, want [Gone]", c.onlyBase)
	}
	if c.failed() {
		t.Errorf("unmatched benchmarks should not fail the gate: %v", c.failures())
	}
	if report := c.markdown(); !strings.Contains(report, "new benchmarks") {
		t.Errorf("report should mention the unmatched benchmark:\n%s", report)
	}
}

// TestReportShowsVerdicts checks that a run too small to judge says so rather
// than printing a green row that was never evaluated.
func TestReportShowsVerdicts(t *testing.T) {
	base := runsOf(runWith("Sparse", sample{nsPerOp: 100}, sample{nsPerOp: 100}))
	head := runsOf(runWith("Sparse", sample{nsPerOp: 400}, sample{nsPerOp: 400}))
	c := compare(base, head, 1.20, nil)
	report := c.markdown()
	if !strings.Contains(report, "not judged") {
		t.Errorf("report should mark the 2-sample benchmark as unjudged:\n%s", report)
	}
	if c.inconclusive() != 1 {
		t.Errorf("inconclusive() = %d, want 1", c.inconclusive())
	}
	if !strings.Contains(report, "fewer than 3 samples") {
		t.Errorf("report should explain the unjudged count:\n%s", report)
	}

	// The same comparison with enough samples is judged, and fails.
	full := compare(runsOf(runOf("Sparse", 100, 0)), runsOf(runOf("Sparse", 400, 0)), 1.20, nil)
	if full.inconclusive() != 0 || !full.failed() {
		t.Errorf("inconclusive, failed = %d, %v want 0, true", full.inconclusive(), full.failed())
	}
}

// TestMissingMeasurements covers the failure that produces no rows at all, and
// therefore no failures, unless something looks for it.
func TestMissingMeasurements(t *testing.T) {
	const bothSides = `pkg: a/one
BenchmarkA-16  100  10.0 ns/op
pkg: a/two
BenchmarkB-16  100  10.0 ns/op
`
	if got := missingMeasurements(bothSides, bothSides); len(got) != 0 {
		t.Errorf("identical runs reported %v, want nothing", got)
	}

	// a/two ran nothing on the head side: its benchmark vanished, which is
	// indistinguishable from a big improvement in the report.
	head := `pkg: a/one
BenchmarkA-16  100  10.0 ns/op
pkg: a/two
`
	got := missingMeasurements(bothSides, head)
	if len(got) != 1 || !strings.Contains(got[0], "a/two") || !strings.Contains(got[0], "head measured none") {
		t.Errorf("missingMeasurements = %v, want one note about a/two on the head side", got)
	}

	// The reverse direction is not a loss: the base ref simply had no such
	// benchmark. It is reported as new coverage, not as a missing measurement.
	if got := missingMeasurements(head, bothSides); len(got) != 0 {
		t.Errorf("missingMeasurements = %v, want nothing when only the base side is short", got)
	}
	got = unmeasuredIsNew(head, bothSides)
	if len(got) != 1 || !strings.Contains(got[0], "a/two") || !strings.Contains(got[0], "new benchmarks") {
		t.Errorf("unmeasuredIsNew = %v, want one note about a/two being new", got)
	}
}

func keysOf(m map[string]*run) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
