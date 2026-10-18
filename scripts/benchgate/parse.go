package main

// Pure parts of the benchmark gate: parsing `go test -bench` output and turning
// two parsed runs into a pass/fail decision. They live here, without a build
// tag, so they can be unit-tested; main.go keeps the parts that shell out to
// git and go, which cannot be tested in-process.
//
// The decision is a one-sided confidence bound rather than a bare ratio: a
// benchmark fails only when the pessimistic end of its interval is still above
// the limit. That is the whole design, and it exists because the simpler rules
// were measured and did not work - see pairedRatio and the notes there.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

type sample struct {
	nsPerOp     float64
	bytesPerOp  float64
	allocsPerOp float64
}

// run is one benchmark measured -count times, kept in measurement order.
//
// The order is the point, not an accident of storage: main.go alternates the
// two sides, so sample i on one side was taken moments from sample i on the
// other. The paired ratio below then compares numbers from the same machine
// state, and drift - a turbo bin change, thermal throttling, a noisy neighbour
// - cancels instead of landing entirely on whichever side happened to run
// second.
type run struct {
	name    string
	samples []sample
}

func (r *run) count() int { return len(r.samples) }

// median is the per-field median. It is what the report shows and what the
// allocation check compares, because an allocation that happens on half the
// iterations is a regression even if the fastest iteration avoided it.
func (r *run) median() sample {
	if len(r.samples) == 0 {
		return sample{}
	}
	ns := make([]float64, len(r.samples))
	bytes := make([]float64, len(r.samples))
	allocs := make([]float64, len(r.samples))
	for i, s := range r.samples {
		ns[i], bytes[i], allocs[i] = s.nsPerOp, s.bytesPerOp, s.allocsPerOp
	}
	sort.Float64s(ns)
	sort.Float64s(bytes)
	sort.Float64s(allocs)
	mid := func(v []float64) float64 { return v[len(v)/2] }
	return sample{nsPerOp: mid(ns), bytesPerOp: mid(bytes), allocsPerOp: mid(allocs)}
}

func parseBenchOutput(out string) map[string]*run {
	collected := map[string][]sample{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasPrefix(fields[0], "Benchmark") {
			continue
		}
		name := stripProcs(fields[0])
		s, ok := parseBenchLine(fields)
		if ok {
			collected[name] = append(collected[name], s)
		}
	}
	runs := make(map[string]*run, len(collected))
	for name, samples := range collected {
		runs[name] = &run{name: name, samples: samples}
	}
	return runs
}

// parseBenchLine reads the metric columns of one result line:
//
//	BenchmarkX-16   2000000   512.3 ns/op   0 B/op   0 allocs/op
//
// They come as value/unit pairs after the iteration count, which has a value
// but no unit. Advance by two when a pair matches and by one otherwise, so the
// first pair does not knock the rest out of alignment, and so an extra column
// (MB/s, for one) cannot shift the ones behind it.
func parseBenchLine(fields []string) (sample, bool) {
	var s sample
	seen := false
	for i := 1; i < len(fields); {
		if i+1 >= len(fields) {
			break
		}
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			i++
			continue
		}
		switch fields[i+1] {
		case "ns/op":
			s.nsPerOp, seen = value, true
		case "B/op":
			s.bytesPerOp = value
		case "allocs/op":
			s.allocsPerOp = value
		default:
			i++
			continue
		}
		i += 2
	}
	return s, seen
}

func stripProcs(name string) string {
	if idx := strings.LastIndex(name, "-"); idx > 0 {
		if _, err := strconv.Atoi(name[idx+1:]); err == nil {
			return name[:idx]
		}
	}
	return name
}

// benchmarksPerPackage counts the benchmark lines under each "pkg:" heading.
func benchmarksPerPackage(out string) map[string]int {
	counts := map[string]int{}
	pkg := ""
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "pkg: "); ok {
			pkg = strings.TrimSpace(rest)
			continue
		}
		if pkg != "" && strings.HasPrefix(line, "Benchmark") {
			counts[pkg]++
		}
	}
	return counts
}

// missingMeasurements reports packages the base side measured and the head side
// did not, and it exists because that failure is invisible by construction.
//
// A package that silently ran nothing - a test binary that failed to start, a
// -bench pattern that stopped matching, a measurement lost to a transient -
// produces no rows, and no rows fail no checks. It looks exactly like a package
// that improved. The gate must not report "no regressions" about a side it
// never measured, so this is checked before anything else is reported.
//
// Only the base -> head direction is fatal. The reverse (head measured a package
// the base did not) is a benchmark that did not exist at the base ref, which is
// how every benchmark is born; unmeasuredIsNew reports it separately as a
// notice instead of failing a merge for adding coverage.
func missingMeasurements(baseOut, headOut string) []string {
	base, head := benchmarksPerPackage(baseOut), benchmarksPerPackage(headOut)
	if len(base) == 0 && len(head) == 0 {
		return nil
	}
	var out []string
	for pkg, n := range base {
		if head[pkg] == 0 {
			out = append(out, fmt.Sprintf("%s: base measured %d benchmarks, head measured none", pkg, n))
		}
	}
	sort.Strings(out)
	return out
}

// unmeasuredIsNew lists the packages that only the head side measured, i.e. the
// benchmarks that have no base value to compare against. The caller prints them
// so the coverage they add is visible, without pretending they were gated.
func unmeasuredIsNew(baseOut, headOut string) []string {
	base, head := benchmarksPerPackage(baseOut), benchmarksPerPackage(headOut)
	var out []string
	for pkg, n := range head {
		if base[pkg] == 0 {
			out = append(out, fmt.Sprintf("%s: %d new benchmarks (no base value, not gated)", pkg, n))
		}
	}
	sort.Strings(out)
	return out
}

// minSamples is the fewest iterations the ns/op decision will act on.
//
// Two samples give a standard error that is a coin flip; below three the gate
// reports the number and refuses to judge it. This is also why -count 1 or 2
// cannot fail on ns/op: the run is a smoke test, not a verdict.
const minSamples = 3

// pairedRatio is the head/base cost ratio, estimated from paired samples, plus
// the uncertainty of that estimate.
type pairedRatio struct {
	// mean is the average of the per-iteration ratios head_i / base_i.
	mean float64
	// half is the half-width of the one-sided 95% interval around mean.
	half float64
	// n is how many pairs the estimate rests on.
	n int
}

// lower is the pessimistic end of the interval: the smallest slowdown that is
// still consistent with what was measured.
func (p pairedRatio) lower() float64 { return p.mean - p.half }

// fails reports whether the benchmark got slower by more than threshold, with
// enough confidence to block a merge.
//
// Three rules were tried against the same control experiment - two builds of one
// commit that differ only in this script - and only this one survived:
//
//  1. Ratio of medians. Produced 6 "regressions" of 20-48% and a dozen
//     "improvements" of 20-46% between identical benchmarks.
//  2. Best-of-N: the minimum of each side, on the theory that interference can
//     only slow a sample down. Refuted by the data - one side's samples read
//     76930, 22875, 30185 ns/op for the same binary, so its minimum is a random
//     draw from a 3.4x range, not a floor. Six false failures remained.
//  3. This: mean of the paired ratios, minus two-ish standard errors. Two false
//     failures remained at -count 3, and both disappear at -count 6, where the
//     standard error is no longer estimated from three points.
//
// The lesson the three attempts share: on a machine whose samples move by
// tens of percent, no point estimate can be trusted, so the gate has to carry
// its own uncertainty and refuse to fail when the uncertainty is larger than
// the effect it is asked to detect.
func (p pairedRatio) fails(threshold float64) bool {
	return p.n >= minSamples && p.lower() > threshold
}

// pairedRatioOf estimates head/base from paired samples.
//
// Mean of ratios rather than ratio of means: the two sides are sampled
// alternately, so pairing the i-th sample of each removes the machine state
// they share. When the two sides have different counts (a benchmark that
// appeared mid-run) the extra samples on the longer side are ignored; the
// counts are equal in practice because main.go drives both sides from one loop.
func pairedRatioOf(base, head []sample) pairedRatio {
	n := len(base)
	if len(head) < n {
		n = len(head)
	}
	if n == 0 {
		return pairedRatio{mean: 1}
	}
	ratios := make([]float64, n)
	var sum float64
	for i := 0; i < n; i++ {
		if base[i].nsPerOp <= 0 {
			return pairedRatio{mean: 1, n: n}
		}
		ratios[i] = head[i].nsPerOp / base[i].nsPerOp
		sum += ratios[i]
	}
	p := pairedRatio{mean: sum / float64(n), n: n}
	if n < 2 {
		return p
	}
	var sq float64
	for _, r := range ratios {
		sq += (r - p.mean) * (r - p.mean)
	}
	se := math.Sqrt(sq/float64(n-1)) / math.Sqrt(float64(n))
	p.half = tCritical(n-1) * se
	return p
}

// tCritical is the one-sided 95% Student-t multiplier for df degrees of freedom.
//
// A fixed multiple of the standard error would be most confident exactly where
// the gate is weakest. At three samples the standard error is itself barely
// more than a guess, and the t distribution is the correction for that: 2.92 at
// df 2 instead of 1.65 at infinity. The table stops at 30, past which the two
// agree to within 2%.
var tCriticalTable = [31]float64{
	0, 6.314, 2.920, 2.353, 2.132, 2.015, 1.943, 1.895, 1.860, 1.833,
	1.812, 1.796, 1.782, 1.771, 1.761, 1.753, 1.746, 1.740, 1.734, 1.729,
	1.725, 1.721, 1.717, 1.714, 1.711, 1.708, 1.706, 1.704, 1.701, 1.699,
	1.697,
}

func tCritical(df int) float64 {
	if df < 1 {
		return tCriticalTable[1]
	}
	if df >= len(tCriticalTable) {
		return 1.645
	}
	return tCriticalTable[df]
}

type row struct {
	name string
	// base and head are the per-field medians of each side, for display.
	base, head sample
	ratio      pairedRatio
	exempt     bool
}

type comparison struct {
	base, head string
	threshold  float64
	rows       []row
	onlyHead   []string
	onlyBase   []string
}

func compare(baseRuns, headRuns map[string]*run, threshold float64, exempt map[string]bool) *comparison {
	c := &comparison{threshold: threshold}
	for name, head := range headRuns {
		base, ok := baseRuns[name]
		if !ok {
			c.onlyHead = append(c.onlyHead, name)
			continue
		}
		c.rows = append(c.rows, row{
			name:   name,
			base:   base.median(),
			head:   head.median(),
			ratio:  pairedRatioOf(base.samples, head.samples),
			exempt: exempt[name],
		})
	}
	for name := range baseRuns {
		if _, ok := headRuns[name]; !ok {
			c.onlyBase = append(c.onlyBase, name)
		}
	}
	sort.Slice(c.rows, func(i, j int) bool {
		if c.rows[i].ratio.mean != c.rows[j].ratio.mean {
			return c.rows[i].ratio.mean > c.rows[j].ratio.mean
		}
		return c.rows[i].name < c.rows[j].name
	})
	sort.Strings(c.onlyHead)
	sort.Strings(c.onlyBase)
	return c
}

// failures lists the benchmarks that block: slower than the threshold with 95%
// confidence, or one extra allocation per op.
//
// Allocations are compared exactly rather than by ratio. They are integer
// counts produced by the runtime, not timings, so 0 -> 1 is a real change that
// no amount of machine noise produces - and an extra allocation per message is
// the failure this whole batch of work exists to remove.
func (c *comparison) failures() []string {
	var out []string
	for _, r := range c.rows {
		if r.exempt {
			continue
		}
		if r.ratio.fails(c.threshold) {
			out = append(out, fmt.Sprintf("%s: %+.1f%% slower, 95%% bound %+.1f%% (limit +%.0f%%)",
				r.name, (r.ratio.mean-1)*100, (r.ratio.lower()-1)*100, (c.threshold-1)*100))
		}
		if r.head.allocsPerOp > r.base.allocsPerOp {
			out = append(out, fmt.Sprintf("%s: allocs/op %g -> %g",
				r.name, r.base.allocsPerOp, r.head.allocsPerOp))
		}
	}
	return out
}

func (c *comparison) failed() bool { return len(c.failures()) > 0 }

// inconclusive counts the benchmarks whose samples were too few to judge.
func (c *comparison) inconclusive() int {
	n := 0
	for _, r := range c.rows {
		if !r.exempt && r.ratio.n < minSamples {
			n++
		}
	}
	return n
}

func (c *comparison) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Benchmark gate\n\n")
	fmt.Fprintf(&b, "base `%s` -> head `%s`, limit +%.0f%%.\n\n", c.base, c.head, (c.threshold-1)*100)
	b.WriteString("Sampling alternates base and head one iteration at a time, and `delta` is the mean\n")
	b.WriteString("of the paired ratios with the one-sided 95% bound beside it. A benchmark fails\n")
	b.WriteString("only when the **bound** is still above the limit, so a benchmark the machine\n")
	b.WriteString("cannot resolve passes and shows a wide bound instead of inventing a verdict.\n")
	b.WriteString("`allocs/op` is compared on the median and needs no statistics: it is an exact\n")
	b.WriteString("integer, and an allocation that happens on half the iterations still fails.\n\n")
	b.WriteString("| benchmark | base ns/op | head ns/op | delta | 95% bound | base allocs | head allocs | verdict |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |\n")
	for _, r := range c.rows {
		verdict := "ok"
		switch {
		case r.ratio.n < minSamples:
			verdict = fmt.Sprintf("n=%d, not judged", r.ratio.n)
		case r.exempt:
			verdict = "exempt"
		case r.ratio.fails(c.threshold) || r.head.allocsPerOp > r.base.allocsPerOp:
			verdict = "**FAIL**"
		}
		fmt.Fprintf(&b, "| %s | %.1f | %.1f | %+.1f%% | %+.1f%% | %g | %g | %s |\n",
			r.name, r.base.nsPerOp, r.head.nsPerOp, (r.ratio.mean-1)*100,
			(r.ratio.lower()-1)*100, r.base.allocsPerOp, r.head.allocsPerOp, verdict)
	}
	if n := c.inconclusive(); n > 0 {
		fmt.Fprintf(&b, "\n%d of %d benchmarks had fewer than %d samples and were not judged on ns/op.\n",
			n, len(c.rows), minSamples)
	}
	if len(c.onlyHead) > 0 {
		fmt.Fprintf(&b, "\nnew benchmarks (no base to compare): %s\n", strings.Join(c.onlyHead, ", "))
	}
	if len(c.onlyBase) > 0 {
		fmt.Fprintf(&b, "\nremoved benchmarks: %s\n", strings.Join(c.onlyBase, ", "))
	}
	if failures := c.failures(); len(failures) > 0 {
		b.WriteString("\n## Failures\n\n")
		for _, f := range failures {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	return b.String()
}
