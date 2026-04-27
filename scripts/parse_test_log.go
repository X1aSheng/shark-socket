package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type TestEvent struct {
	Time    string  `json:"Time"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

type testResult struct {
	action  string
	elapsed float64
	outputs []string
}

type benchResult struct {
	name     string
	iter     int64
	nsPerOp  float64
	memPerOp string
	allocs   string
	extra    string
}

var (
	reBenchHeader = regexp.MustCompile(`^(Benchmark\S+?)-\d+\s*$`)
	reBenchData   = regexp.MustCompile(`^\s*(\d+)\s+([\d.]+)\s+ns/op`)
	reBenchMem    = regexp.MustCompile(`\s+([\d.]+)\s+(B/op|KB/op|MB/op)`)
	reBenchAllocs = regexp.MustCompile(`\s+(\d+)\s+allocs/op`)
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: parse_test_log <file.json> [file2.json ...]\n")
		os.Exit(1)
	}

	for _, path := range os.Args[1:] {
		if err := parseFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "error parsing %s: %v\n", path, err)
			os.Exit(1)
		}
	}
}

func parseFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	tests := make(map[string]*testResult)
	var pkgOrder []string
	pkgSet := make(map[string]bool)
	coverages := make(map[string]string)
	startTime := ""

	// Benchmark tracking: collect output lines per package, then parse
	pkgOutputs := make(map[string][]string)
	var benchPkgOrder []string
	benchPkgSet := make(map[string]bool)
	pkgElapsed := make(map[string]float64)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev TestEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}

		if startTime == "" && ev.Time != "" {
			startTime = ev.Time
		}

		key := ev.Package + "/" + ev.Test

		switch ev.Action {
		case "run":
			if ev.Test != "" {
				tests[key] = &testResult{action: "run", outputs: []string{}}
			}
			if !pkgSet[ev.Package] && ev.Package != "" {
				pkgSet[ev.Package] = true
				pkgOrder = append(pkgOrder, ev.Package)
			}
		case "pass", "fail", "skip":
			if ev.Test != "" {
				if t, ok := tests[key]; ok {
					t.action = ev.Action
					t.elapsed = ev.Elapsed
				} else {
					tests[key] = &testResult{action: ev.Action, elapsed: ev.Elapsed}
				}
			}
			if ev.Test == "" && ev.Package != "" {
				pkgElapsed[ev.Package] = ev.Elapsed
			}
		case "output":
			if ev.Test != "" {
				if t, ok := tests[key]; ok {
					t.outputs = append(t.outputs, ev.Output)
				}
			}
			// Collect all output for benchmark parsing
			if ev.Package != "" {
				pkgOutputs[ev.Package] = append(pkgOutputs[ev.Package], ev.Output)
				if !benchPkgSet[ev.Package] {
					benchPkgSet[ev.Package] = true
					benchPkgOrder = append(benchPkgOrder, ev.Package)
				}
			}
			// Capture coverage lines
			if strings.Contains(ev.Output, "coverage:") {
				pkg := ev.Package
				coverage := strings.TrimSpace(strings.TrimPrefix(ev.Output, "coverage:"))
				if pkg != "" && coverage != "" {
					coverages[pkg] = coverage
				}
			}
		}
	}

	// Determine test type from filename
	base := filepath.Base(path)
	testType := "Tests"
	isBenchmark := false
	if strings.Contains(base, "unit") {
		testType = "Unit Tests"
	} else if strings.Contains(base, "integration") {
		testType = "Integration Tests"
	} else if strings.Contains(base, "benchmark") {
		testType = "Benchmark Tests"
		isBenchmark = true
	}

	// Format start time
	ts := "unknown"
	if startTime != "" {
		if t, err := time.Parse(time.RFC3339Nano, startTime); err == nil {
			ts = t.Format("2006-01-02 15:04:05")
		}
	}

	// Print report
	sep := strings.Repeat("=", 70)
	sep2 := strings.Repeat("-", 70)

	fmt.Println(sep)
	fmt.Printf("  shark-socket test report\n")
	fmt.Printf("  %s — %s\n", testType, ts)
	fmt.Println(sep)
	fmt.Println()

	// Print unit/integration tests (if any)
	if len(pkgOrder) > 0 {
		passCount, failCount, skipCount := 0, 0, 0
		totalElapsed := 0.0

		for _, pkg := range pkgOrder {
			pkgShort := shortPkg(pkg)
			fmt.Printf("  [%s]\n", pkgShort)

			for key, t := range tests {
				if !strings.HasPrefix(key, pkg+"/") || key == pkg+"/" {
					continue
				}
				status := strings.ToUpper(t.action)
				testName := evTest(key)
				padded := fmt.Sprintf("%-5s %-55s", status, testName)
				elapsedStr := ""
				if t.elapsed > 0 {
					elapsedStr = fmt.Sprintf("%.2fs", t.elapsed)
					totalElapsed += t.elapsed
				}
				fmt.Printf("    %s %s\n", padded, elapsedStr)

				switch t.action {
				case "pass":
					passCount++
				case "fail":
					failCount++
					for _, out := range t.outputs {
						if strings.TrimSpace(out) != "" {
							fmt.Printf("        >> %s", out)
						}
					}
				case "skip":
					skipCount++
				}
			}
			fmt.Println()
		}

		fmt.Println(sep2)
		fmt.Printf("  Summary: %d passed, %d failed, %d skipped\n", passCount, failCount, skipCount)
		fmt.Printf("  Duration: %.2fs\n", totalElapsed)

		if len(coverages) > 0 {
			fmt.Println()
			fmt.Println("  Coverage:")
			for _, pkg := range pkgOrder {
				if cov, ok := coverages[pkg]; ok {
					fmt.Printf("    %-40s %s\n", shortPkg(pkg), cov)
				}
			}
		}

		fmt.Println(sep)
		fmt.Println()
	}

	// Print benchmark results (if benchmark type)
	if isBenchmark {
		totalBench := 0
		totalDuration := 0.0

		fmt.Println(sep)
		fmt.Printf("  shark-socket benchmark report\n")
		fmt.Printf("  %s\n", ts)
		fmt.Println(sep)
		fmt.Println()

		for _, pkg := range benchPkgOrder {
			outputs := pkgOutputs[pkg]
			benches := parseBenchOutputs(outputs)
			if len(benches) == 0 {
				continue
			}

			pkgShort := shortPkg(pkg)
			fmt.Printf("  [%s]\n", pkgShort)

			for _, b := range benches {
				totalBench++
				fmt.Printf("    %-50s %10s ns/op\n", b.name, formatFloat(b.nsPerOp))
				if b.memPerOp != "" || b.allocs != "" {
					fmt.Printf("      %-48s %10s  %s allocs\n", "", b.memPerOp, b.allocs)
				}
				if b.extra != "" {
					fmt.Printf("      %-48s %s\n", "", b.extra)
				}
			}
			if elapsed, ok := pkgElapsed[pkg]; ok {
				totalDuration += elapsed
				fmt.Printf("\n    Package duration: %.2fs\n", elapsed)
			}
			fmt.Println()
		}

		fmt.Println(sep2)
		fmt.Printf("  Benchmarks: %d\n", totalBench)
		fmt.Printf("  Total duration: %.2fs\n", totalDuration)
		fmt.Println(sep)
		fmt.Println()
	}

	return nil
}

func parseBenchOutputs(outputs []string) []benchResult {
	var results []benchResult

	// Join all output for this package into lines
	var lines []string
	for _, o := range outputs {
		trimmed := strings.TrimRight(o, "\n\r")
		if trimmed == "" {
			continue
		}
		// Skip non-benchmark lines
		if strings.HasPrefix(trimmed, "goos:") ||
			strings.HasPrefix(trimmed, "goarch:") ||
			strings.HasPrefix(trimmed, "pkg:") ||
			strings.HasPrefix(trimmed, "cpu:") ||
			strings.HasPrefix(trimmed, "ok ") ||
			strings.HasPrefix(trimmed, "FAIL") ||
			strings.HasPrefix(trimmed, "PASS") ||
			strings.HasPrefix(trimmed, "coverage:") ||
			strings.Contains(trimmed, "server listening") ||
			strings.Contains(trimmed, "TCP server") ||
			strings.Contains(trimmed, "UDP server") {
			continue
		}
		lines = append(lines, trimmed)
	}

	// Parse benchmark header + data pairs
	// Go test -json splits the output into multiple output events:
	//   Event 1: "BenchmarkName-16    \t"  (header with name and iterations)
	//   Event 2: "    5446\t    268094 ns/op\t..." (data line with metrics)
	// OR sometimes combined.
	//
	// We look for lines matching "BenchmarkXxx-NN" pattern followed by data with ns/op.

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Try to match a benchmark result line:
		// "BenchmarkTCPEcho-16                           \t    5446\t    268094 ns/op\t..."
		// or split across two output events

		// Check if this line is a bench header (name + iter count, no ns/op)
		if strings.Contains(line, "ns/op") {
			// This line already has the data — try to extract from combined line
			// Format: "BenchmarkName-16  \t<iter>\t<ns/op> ..."
			b := parseBenchLine(line)
			if b != nil {
				results = append(results, *b)
			}
			continue
		}

		// Header line without data — check if next line has the data
		if reBenchHeader.MatchString(strings.TrimSpace(line)) {
			name := extractBenchName(strings.TrimSpace(line))
			if name == "" {
				continue
			}
			// Look at next line for data
			if i+1 < len(lines) {
				nextLine := lines[i+1]
				if strings.Contains(nextLine, "ns/op") {
					b := parseBenchDataLine(name, nextLine)
					if b != nil {
						results = append(results, *b)
					}
					i++ // skip the data line
				}
			}
		}
	}

	return results
}

func extractBenchName(line string) string {
	// "BenchmarkSessionManager_NextID-16             \t"
	// Remove trailing tabs/spaces first
	line = strings.TrimRight(line, "\t ")
	parts := strings.SplitN(line, "-", 2)
	if len(parts) < 2 {
		return ""
	}
	name := parts[0]
	if !strings.HasPrefix(name, "Benchmark") {
		return ""
	}
	return name
}

func parseBenchLine(line string) *benchResult {
	// Combined line: "BenchmarkTCPEcho-16    \t5446\t    268094 ns/op\t    7581 B/op\t      66 allocs/op"
	// Or just the header part without data (handled elsewhere)

	// Extract name
	name := extractBenchName(strings.TrimSpace(line))
	if name == "" {
		return nil
	}

	// Find the data part after the name
	// Split by the name part to get the remainder
	idx := strings.Index(line, "\t")
	if idx < 0 {
		return nil
	}
	// Get everything after the tab(s) following the name
	remainder := line[idx:]
	remainder = strings.TrimLeft(remainder, "\t ")
	return parseBenchDataLine(name, remainder)
}

func parseBenchDataLine(name, line string) *benchResult {
	// Line format: "    5446\t    268094 ns/op\t    7581 B/op\t      66 allocs/op"
	// Or: "   22442\t     52788 ns/op\t 155.19 MB/s\t   72530 B/op\t      14 allocs/op"

	b := &benchResult{name: name}

	// Extract ns/op
	m := reBenchData.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	if _, err := fmt.Sscanf(m[1], "%d", &b.iter); err != nil {
		return nil
	}
	if _, err := fmt.Sscanf(m[2], "%f", &b.nsPerOp); err != nil {
		return nil
	}

	// Extract memory
	memMatch := reBenchMem.FindStringSubmatch(line)
	if memMatch != nil {
		b.memPerOp = memMatch[1] + " " + memMatch[2]
	}

	// Extract allocs
	allocMatch := reBenchAllocs.FindStringSubmatch(line)
	if allocMatch != nil {
		b.allocs = allocMatch[1]
	}

	// Check for throughput (MB/s) or other extra metrics
	// Re-parse to find MB/s
	tpRegex := regexp.MustCompile(`([\d.]+)\s+MB/s`)
	tpMatch := tpRegex.FindStringSubmatch(line)
	if tpMatch != nil {
		b.extra = tpMatch[1] + " MB/s"
	}

	return b
}

func formatFloat(v float64) string {
	if v < 1 {
		return fmt.Sprintf("%.3f", v)
	}
	if v < 1000 {
		return fmt.Sprintf("%.1f", v)
	}
	if v < 1_000_000 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.0f", v)
}

func shortPkg(pkg string) string {
	parts := strings.Split(pkg, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return pkg
}

func evTest(key string) string {
	idx := strings.Index(key, "/")
	if idx >= 0 {
		return key[idx+1:]
	}
	return key
}
