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

const modulePrefix = "github.com/X1aSheng/shark-socket-new/"

type testEvent struct {
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
	nsPerOp  string
	memPerOp string
	allocs   string
}

var (
	benchCombined = regexp.MustCompile(`^(Benchmark\S+?)-\d+\s+\d+\s+([\d.]+)\s+ns/op(?:\s+([\d.]+)\s+B/op)?(?:\s+(\d+)\s+allocs/op)?`)
	benchHeader   = regexp.MustCompile(`^(Benchmark\S+?)-\d+\s*$`)
	benchData     = regexp.MustCompile(`^\d+\s+([\d.]+)\s+ns/op(?:\s+([\d.]+)\s+B/op)?(?:\s+(\d+)\s+allocs/op)?`)
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: parse_test_log <file.json> [file.json...]")
		os.Exit(1)
	}
	for _, path := range os.Args[1:] {
		if err := parseFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "parse %s: %v\n", path, err)
			os.Exit(1)
		}
	}
}

func parseFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	tests := map[string]*testResult{}
	packageSeen := map[string]bool{}
	var packages []string
	packageElapsed := map[string]float64{}
	benchmarks := map[string][]benchResult{}
	benchPending := map[string]string{}
	var started string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var ev testEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		if started == "" && ev.Time != "" {
			started = normalizeTime(ev.Time)
		}
		pkg := stripModule(ev.Package)
		if pkg != "" && !packageSeen[pkg] {
			packageSeen[pkg] = true
			packages = append(packages, pkg)
		}
		key := pkg + "/" + ev.Test
		switch ev.Action {
		case "run":
			if ev.Test != "" {
				tests[key] = &testResult{action: "run"}
			}
		case "pass", "fail", "skip":
			if ev.Test == "" {
				packageElapsed[pkg] = ev.Elapsed
				continue
			}
			if tests[key] == nil {
				tests[key] = &testResult{}
			}
			tests[key].action = ev.Action
			tests[key].elapsed = ev.Elapsed
		case "output":
			if ev.Test != "" {
				if tests[key] == nil {
					tests[key] = &testResult{}
				}
				tests[key].outputs = append(tests[key].outputs, ev.Output)
			}
			if b := parseBench(ev.Output, benchPending[pkg]); b.name != "" {
				benchmarks[pkg] = append(benchmarks[pkg], b)
				benchPending[pkg] = ""
				continue
			}
			if name := parseBenchHeader(ev.Output); name != "" {
				benchPending[pkg] = name
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	title := reportTitle(path)
	sep := strings.Repeat("=", 72)
	fmt.Println(sep)
	fmt.Printf("  shark-socket-new %s\n", title)
	fmt.Printf("  started: %s\n", started)
	fmt.Println(sep)

	pass, fail, skip := 0, 0, 0
	for _, pkg := range packages {
		fmt.Printf("\n  [%s]\n", pkg)
		for key, result := range tests {
			if !strings.HasPrefix(key, pkg+"/") || strings.HasSuffix(key, "/") {
				continue
			}
			name := strings.TrimPrefix(key, pkg+"/")
			status := strings.ToUpper(result.action)
			if status == "" {
				status = "RUN"
			}
			fmt.Printf("    %-5s %-56s %.3fs\n", status, name, result.elapsed)
			switch result.action {
			case "pass":
				pass++
			case "fail":
				fail++
				for _, line := range result.outputs {
					if strings.TrimSpace(line) != "" {
						fmt.Printf("      >> %s", line)
					}
				}
			case "skip":
				skip++
			}
		}
		for _, bench := range benchmarks[pkg] {
			fmt.Printf("    BENCH %-54s %s ns/op", bench.name, bench.nsPerOp)
			if bench.memPerOp != "" || bench.allocs != "" {
				fmt.Printf("  %s B/op  %s allocs/op", bench.memPerOp, bench.allocs)
			}
			fmt.Println()
		}
		if elapsed, ok := packageElapsed[pkg]; ok {
			fmt.Printf("    package elapsed: %.3fs\n", elapsed)
		}
	}
	fmt.Println()
	fmt.Printf("  summary: %d passed, %d failed, %d skipped\n", pass, fail, skip)
	fmt.Println(sep)
	return nil
}

func parseBench(output, pendingName string) benchResult {
	line := strings.TrimSpace(output)
	if match := benchCombined.FindStringSubmatch(line); match != nil {
		return benchResult{name: match[1], nsPerOp: match[2], memPerOp: match[3], allocs: match[4]}
	}
	if pendingName == "" {
		return benchResult{}
	}
	match := benchData.FindStringSubmatch(line)
	if match == nil {
		return benchResult{}
	}
	return benchResult{name: pendingName, nsPerOp: match[1], memPerOp: match[2], allocs: match[3]}
}

func parseBenchHeader(output string) string {
	line := strings.TrimSpace(output)
	match := benchHeader.FindStringSubmatch(line)
	if match == nil {
		return ""
	}
	return match[1]
}

func reportTitle(path string) string {
	name := filepath.Base(path)
	switch {
	case strings.Contains(name, "benchmark"):
		return "benchmark report"
	case strings.Contains(name, "integration"):
		return "integration test report"
	case strings.Contains(name, "unit"):
		return "unit test report"
	case strings.Contains(name, "race"):
		return "race test report"
	default:
		return "test report"
	}
}

func stripModule(pkg string) string {
	return strings.TrimPrefix(pkg, modulePrefix)
}

func normalizeTime(raw string) string {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	return parsed.Format("2006-01-02T15:04:05.000")
}
