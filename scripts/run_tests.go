//go:build ignore

// shark-socket cross-platform test runner.
//
// Usage:
//
//	go run scripts/run_tests.go                     # run all
//	go run scripts/run_tests.go -mode unit          # unit tests only
//	go run scripts/run_tests.go -mode integration   # integration tests only
//	go run scripts/run_tests.go -mode benchmark     # benchmarks only
//	go run scripts/run_tests.go -mode cover         # coverage report
//
// All logs are saved under ./logs/ with timestamped filenames.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	mode    = flag.String("mode", "all", "test mode: unit, integration, benchmark, cover, all")
	logDir  = flag.String("logdir", "logs", "directory to save log files")
	timeout = flag.Duration("timeout", 5*time.Minute, "test timeout")
)

func main() {
	flag.Parse()

	// Resolve project root (two levels up from this file)
	exe, _ := os.Executable()
	projectDir := filepath.Clean(filepath.Join(filepath.Dir(exe), "..", ".."))
	if _, err := os.Stat(filepath.Join(projectDir, "go.mod")); err != nil {
		// Fallback: use cwd
		projectDir, _ = os.Getwd()
	}

	logsDir := filepath.Join(projectDir, *logDir)
	os.MkdirAll(logsDir, 0o755)

	ts := time.Now().Format("20060102_150405")

	switch *mode {
	case "unit":
		runTests(projectDir, logsDir, ts, "unit", "Unit Tests",
			"./internal/...", "./api/...", "./tests/unit/...",
		)
	case "integration":
		runTests(projectDir, logsDir, ts, "integration", "Integration Tests",
			"./tests/integration/...",
		)
	case "benchmark":
		runBenchmarks(projectDir, logsDir, ts)
	case "cover":
		runCoverage(projectDir, logsDir, ts)
	case "all":
		fmt.Println()
		printHeader("shark-socket full test suite", time.Now().Format("2006-01-02 15:04:05"))

		runTests(projectDir, logsDir, ts, "unit", "Unit Tests",
			"./internal/...", "./api/...", "./tests/unit/...",
		)
		runTests(projectDir, logsDir, ts, "integration", "Integration Tests",
			"./tests/integration/...",
		)
		runBenchmarks(projectDir, logsDir, ts)

		fmt.Println()
		printHeader("All tests complete", "")
		fmt.Printf("  Logs in: %s\n", logsDir)
		listFiles(logsDir, ts)
	default:
		fmt.Fprintf(os.Stderr, "Unknown mode: %s\n", *mode)
		fmt.Fprintln(os.Stderr, "Usage: go run scripts/run_tests.go -mode [unit|integration|benchmark|cover|all]")
		os.Exit(1)
	}
}

func runTests(projectDir, logsDir, ts, name, label string, packages ...string) {
	jsonFile := filepath.Join(logsDir, fmt.Sprintf("%s_%s.json", ts, name))
	logFile := filepath.Join(logsDir, fmt.Sprintf("%s_%s.log", ts, name))

	fmt.Println()
	printInfo(">>>", fmt.Sprintf("[%s] Running...", label))
	printInfo(">>>", fmt.Sprintf("JSON: %s", jsonFile))
	printInfo(">>>", fmt.Sprintf("Log:  %s", logFile))
	fmt.Println()

	args := []string{"test", "-json", "-v", "-count=1", fmt.Sprintf("-timeout=%s", *timeout)}
	args = append(args, packages...)
	runGo(projectDir, args, jsonFile)

	// Parse JSON to readable log
	parseArgs := []string{"run", "scripts/parse_test_log.go", jsonFile}
	cmd := exec.Command("go", parseArgs...)
	cmd.Dir = projectDir
	out, _ := cmd.CombinedOutput()
	os.WriteFile(logFile, out, 0o644)

	if len(out) > 0 {
		fmt.Println()
		fmt.Print(string(out))
	}

	fmt.Println()
	printSuccess(fmt.Sprintf("[%s] Done. Log saved.", label))
	fmt.Println()
}

func runBenchmarks(projectDir, logsDir, ts string) {
	packages := []string{
		"./tests/benchmark/...",
		"./internal/protocol/tcp/...",
		"./internal/infra/bufferpool/...",
		"./internal/session/...",
		"./internal/plugin/...",
	}

	jsonFile := filepath.Join(logsDir, fmt.Sprintf("%s_benchmark.json", ts))
	logFile := filepath.Join(logsDir, fmt.Sprintf("%s_benchmark.log", ts))

	fmt.Println()
	printInfo(">>>", "[Benchmarks] Running...")
	printInfo(">>>", fmt.Sprintf("JSON: %s", jsonFile))
	printInfo(">>>", fmt.Sprintf("Log:  %s", logFile))
	fmt.Println()

	args := []string{"test", "-bench=.", "-benchmem", "-run=^$", "-count=1", fmt.Sprintf("-timeout=%s", *timeout), "-json"}
	args = append(args, packages...)
	runGo(projectDir, args, jsonFile)

	parseArgs := []string{"run", "scripts/parse_test_log.go", jsonFile}
	cmd := exec.Command("go", parseArgs...)
	cmd.Dir = projectDir
	out, _ := cmd.CombinedOutput()
	os.WriteFile(logFile, out, 0o644)

	if len(out) > 0 {
		fmt.Println()
		fmt.Print(string(out))
	}

	fmt.Println()
	printSuccess("[Benchmarks] Done. Log saved.")
	fmt.Println()
}

func runCoverage(projectDir, logsDir, ts string) {
	logFile := filepath.Join(logsDir, fmt.Sprintf("%s_cover.log", ts))

	fmt.Println()
	printHeader("Coverage Report", time.Now().Format("2006-01-02 15:04:05"))

	args := []string{"test", "./...", "-count=1", "-cover", fmt.Sprintf("-timeout=%s", *timeout)}
	var buf bytes.Buffer
	cmd := exec.Command("go", args...)
	cmd.Dir = projectDir
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	_ = cmd.Run()

	output := buf.String()
	fmt.Print(output)
	os.WriteFile(logFile, []byte(output), 0o644)

	fmt.Println()
	printSuccess(fmt.Sprintf("Coverage log: %s", logFile))
}

func runGo(dir string, args []string, outputFile string) {
	var buf bytes.Buffer

	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	_ = cmd.Run()

	os.WriteFile(outputFile, buf.Bytes(), 0o644)
}

func listFiles(dir, ts string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ts) {
			info, _ := e.Info()
			fmt.Printf("    %s  %s\n", fmtSize(info.Size()), filepath.Join(dir, e.Name()))
		}
	}
}

func fmtSize(n int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
	)
	switch {
	case n >= MB:
		return fmt.Sprintf("%5.1fM", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%5.1fK", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%5dB", n)
	}
}

func printHeader(title, subtitle string) {
	fmt.Println("========================================")
	fmt.Printf("  %s\n", title)
	if subtitle != "" {
		fmt.Printf("  %s\n", subtitle)
	}
	fmt.Println("========================================")
}

func printInfo(prefix, msg string) {
	fmt.Printf("\x1b[36m%s %s\x1b[0m\n", prefix, msg)
}

func printSuccess(msg string) {
	fmt.Printf("\x1b[32m%s\x1b[0m\n", msg)
}
