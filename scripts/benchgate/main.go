// shark-socket benchmark regression gate.
//
// Runs a fixed set of CPU-only microbenchmarks against the merge base and
// against the working tree, then fails when a benchmark got slower than
// -threshold or started allocating more.
//
// Why CPU-only: the socket benchmarks (BenchmarkTCPEcho and friends) move by
// tens of percent between runs on a shared CI runner. A gate built on them
// either flakes until someone raises the threshold, or gets ignored. The
// benchmarks below allocate, parse, lock and copy - which is exactly what the
// data-plane changes move - and repeat to within a few percent.
//
// Why not benchstat: it answers the same question - it also subtracts noise and
// refuses to call something a difference when it cannot - but its output is a
// rendered table, so a gate built on it would have to screen-scrape a format
// that changes with -count and the available metrics. This reports the same
// three numbers (delta, bound, verdict) in a form a program can read, adds the
// allocation rule and the exemption list, and needs no tool installed in CI.
// Both raw runs are written to -out, so `benchstat base.txt head.txt` still
// works by hand when someone wants the full statistics.
//
// Usage:
//
//	go run ./scripts/benchgate                        # base = origin/main or main
//	go run ./scripts/benchgate -base HEAD~1
//	go run ./scripts/benchgate -count 8 -threshold 1.15
//	go run ./scripts/benchgate -allow BenchmarkBroadcastFanOut
//
// A package directory rather than a single ignored file because the parsing
// and comparison helpers need unit tests, and `go run file.go` compiles that
// one file alone: the helpers would be invisible to it.
package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// benchSuite is the fixed set the gate measures. Anything that opens a socket
// is deliberately absent; see the header.
type benchSuite struct {
	pkg     string
	pattern string
}

var gateSuite = []benchSuite{
	{
		pkg:     "./tests/benchmark",
		pattern: `^(BenchmarkSessionManager_|BenchmarkPluginChain_|BenchmarkBroadcastFanOut$|BenchmarkSendCopy$|BenchmarkReadLoopFrameAllocation$|BenchmarkFrameSplitAcrossSegments$|BenchmarkTCPFramerReadCost$)`,
	},
	{
		pkg:     "./internal/transport/coap",
		pattern: `^Benchmark(MessageParse|MessageMarshal|Encode|ReadOption|SortedOptionNums)`,
	},
	{
		pkg:     "./internal/transport/tcp",
		pattern: `^Benchmark(LengthPrefixFramerRoundTrip|LineFramerRoundTrip)`,
	},
	{
		pkg:     "./internal/transport/udp",
		pattern: `^Benchmark(UDPSessionLookup|UDPAddrStringKey)`,
	},
	{
		pkg:     "./internal/plugin",
		pattern: `^BenchmarkRateLimitOnMessage`,
	},
}

func main() {
	os.Exit(gate(os.Args[1:]))
}

// gate holds the body so that os.Exit happens in main, after the deferred
// cleanup. Exiting from inside would leave the extracted base tree behind on
// exactly the runs somebody wants to inspect: the failing ones.
//
// Exit codes: 0 no regressions, 1 the gate failed, 2 the gate could not run.
func gate(args []string) int {
	fs := flag.NewFlagSet("benchgate", flag.ContinueOnError)
	base := fs.String("base", "", "git ref to compare against (default: origin/main, main, then HEAD~1)")
	count := fs.Int("count", 6, "iterations per benchmark; ns/op is only judged at 3 or more")
	benchtime := fs.String("benchtime", "100ms", "benchtime per iteration")
	threshold := fs.Float64("threshold", 1.20, "fail when the lower 95% bound is this many times base")
	out := fs.String("out", "logs/bench-gate", "directory for base.txt, head.txt and report.md")
	allow := fs.String("allow", "", "comma-separated benchmark names exempt from the ns/op check")
	keepTree := fs.Bool("keep", false, "keep the extracted base tree for inspection")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *count < 1 {
		return failf("-count must be >= 1")
	}
	if *threshold <= 1 {
		return failf("-threshold must be > 1 (it is a ratio, not a percentage)")
	}
	if *count < minSamples {
		fmt.Fprintf(os.Stderr, "bench gate: warning: -count %d is below %d, so no benchmark will be judged on ns/op\n",
			*count, minSamples)
	}
	exempt := map[string]bool{}
	for _, name := range strings.Split(*allow, ",") {
		if name = strings.TrimSpace(name); name != "" {
			exempt[name] = true
		}
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		return failf("mkdir %s: %v", *out, err)
	}

	baseRef := *base
	if baseRef == "" {
		baseRef = resolveBaseRef()
		if baseRef == "" {
			return failf("cannot find a base ref; pass -base explicitly")
		}
	}
	baseCommit, err := gitOutput("rev-parse", "--short", baseRef)
	if err != nil {
		return failf("resolve base %q: %v", baseRef, err)
	}
	headCommit, err := gitOutput("rev-parse", "--short", "HEAD")
	if err != nil {
		return failf("resolve HEAD: %v", err)
	}
	fmt.Printf("base  %s (%s)\nhead  %s\n", baseRef, baseCommit, headCommit)

	baseDir, cleanup, err := exportTree(baseRef, *keepTree)
	if err != nil {
		return failf("%v", err)
	}
	defer cleanup()

	opts := benchOptions{count: *count, benchtime: *benchtime}
	baseOut, headOut, err := runPair(baseDir, ".", opts)
	if err != nil {
		return failf("%v", err)
	}
	if err := os.WriteFile(filepath.Join(*out, "base.txt"), []byte(baseOut), 0o644); err != nil {
		return failf("write base.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(*out, "head.txt"), []byte(headOut), 0o644); err != nil {
		return failf("write head.txt: %v", err)
	}

	// Before judging anything: a side that measured nothing cannot be compared,
	// and "no rows" would otherwise sail through as "no regressions".
	if missing := missingMeasurements(baseOut, headOut); len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "bench gate: the two sides did not measure the same packages:\n")
		for _, m := range missing {
			fmt.Fprintf(os.Stderr, "  %s\n", m)
		}
		return failf("re-run before trusting this gate; raw runs in %s", *out)
	}

	baseRuns := parseBenchOutput(baseOut)
	headRuns := parseBenchOutput(headOut)
	if len(headRuns) == 0 {
		return failf("no benchmark results parsed from the head run")
	}

	report := compare(baseRuns, headRuns, *threshold, exempt)
	report.base, report.head = baseCommit, headCommit
	md := report.markdown()
	if err := os.WriteFile(filepath.Join(*out, "report.md"), []byte(md), 0o644); err != nil {
		return failf("write report.md: %v", err)
	}
	fmt.Print(md)
	fmt.Printf("\nraw runs: %s/base.txt %s/head.txt\n", *out, *out)

	if report.failed() {
		return 1
	}
	fmt.Println("bench gate: no regressions")
	return 0
}

type benchOptions struct {
	count     int
	benchtime string
}

// RunPair samples both sides alternately, swapping which side goes first.
//
// Alternating rather than running one side to completion first is what makes
// the comparison usable on a shared machine: the two runs see the same
// frequency bins, the same cache pressure and the same neighbours, so pairing
// sample i with sample i cancels the drift instead of charging it to whichever
// side ran second. Running base for six iterations and then head for six
// measures the machine, not the change - on a 16-thread desktop that alone
// produced 20-45% "regressions" between two commits whose only difference was
// an unused HTTP handler.
//
// Swapping the order each iteration removes the smaller bias that would
// otherwise survive: whichever side goes first sits closer to the previous
// package's last measurement, and pays for the machine settling after it.
func runPair(baseDir, headDir string, opts benchOptions) (baseOut, headOut string, err error) {
	var base, head strings.Builder
	for _, s := range gateSuite {
		fmt.Printf("  %s\n", s.pkg)
		// Build both test binaries before the first timed run. Otherwise the
		// first iteration of each package measures a compile as well, and the
		// compiler's fingerprints would land inside one sample of one side.
		for _, dir := range []string{baseDir, headDir} {
			if err := warmBuild(dir, s); err != nil {
				return "", "", err
			}
		}
		for i := 0; i < opts.count; i++ {
			dirs := []string{baseDir, headDir}
			out := []*strings.Builder{&base, &head}
			labels := []string{"base", "head"}
			if i%2 == 1 {
				dirs[0], dirs[1] = dirs[1], dirs[0]
				out[0], out[1] = out[1], out[0]
				labels[0], labels[1] = labels[1], labels[0]
			}
			for k := 0; k < 2; k++ {
				got, err := runOnce(dirs[k], s, opts)
				if err != nil {
					return "", "", fmt.Errorf("%s %s (iteration %d): %w", labels[k], s.pkg, i+1, err)
				}
				out[k].WriteString(got)
			}
		}
	}
	return base.String(), head.String(), nil
}

// warmBuild compiles the package's test binary without running anything, so the
// build cache is warm before sampling starts.
func warmBuild(dir string, suite benchSuite) error {
	args := []string{"test", suite.pkg, "-run", "^$", "-bench", "^$", "-count", "1", "-timeout", "5m"}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("warm build %s in %s: %v\n%s", suite.pkg, dir, err, tail(string(out), 20))
	}
	return nil
}

func runOnce(dir string, suite benchSuite, opts benchOptions) (string, error) {
	args := []string{
		"test", suite.pkg,
		"-run", "^$",
		"-bench", suite.pattern,
		"-benchmem",
		"-benchtime", opts.benchtime,
		"-count", "1",
		"-timeout", "5m",
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %v\n%s", strings.Join(args, " "), err, tail(string(out), 40))
	}
	return string(out), nil
}

// resolveBaseRef picks a default base commit, or "" when the repository has
// none of the usual ones. Returning the empty string rather than exiting keeps
// the choice of exit code with gate.
func resolveBaseRef() string {
	for _, ref := range []string{"origin/main", "main", "HEAD~1"} {
		if _, err := gitOutput("rev-parse", "--verify", ref); err == nil {
			return ref
		}
	}
	return ""
}

// exportTree materialises the base commit in a temp directory so both sides can
// be built without touching the working tree.
//
// `git archive` rather than `git worktree add` on purpose: a worktree writes
// refs and lock files inside .git, which on a developer machine can collide
// with anything else holding them (an IDE's git integration, an indexer) and
// then fails with "Permission denied" on a lock the script never asked for.
// Archive only reads the object store and its output is the tracked tree
// alone, so no build artifacts, logs or .git come along.
func exportTree(ref string, keep bool) (string, func(), error) {
	dir, err := os.MkdirTemp("", "shark-bench-base-")
	if err != nil {
		return "", nil, fmt.Errorf("temp dir: %w", err)
	}
	cmd := exec.Command("git", "archive", "--format=tar", ref)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("git archive pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("git archive %s: %w", ref, err)
	}
	extractErr := extractTar(stdout, dir)
	// Drain what the tar reader stops at: a tar stream ends with two zero
	// blocks, and git keeps writing the padding after them. Closing the read
	// side while git is blocked on a full pipe deadlocks Wait - it waits for
	// git, git waits for a reader.
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	if extractErr != nil || waitErr != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("git archive %s: %v %v\n%s", ref, extractErr, waitErr, tail(stderr.String(), 20))
	}
	cleanup := func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}
	return dir, cleanup, nil
}

// extractTar unpacks a git archive stream. Only regular files and directories
// are written: a symlink in a future tree would otherwise be able to point
// outside the extraction directory.
func extractTar(r io.Reader, dir string) error {
	reader := tar.NewReader(r)
	for {
		hdr, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, reader); err != nil {
				_ = file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		}
	}
}

func safeJoin(dir, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("archive entry %q escapes the extraction root", name)
	}
	return filepath.Join(dir, clean), nil
}

func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// Never fold stderr into the value: git prints advisory warnings there
		// ("unable to find all commit-graph files"), and one of them in a
		// "commit sha" is a debugging session nobody asked for.
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func tail(s string, lines int) string {
	parts := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

// failf reports a reason the gate could not run and gives the exit code for it.
func failf(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "bench gate: "+format+"\n", args...)
	return 2
}
