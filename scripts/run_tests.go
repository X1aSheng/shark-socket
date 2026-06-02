//go:build ignore

// shark-socket scripted test runner.
//
// Examples:
//   go run scripts/run_tests.go
//   go run scripts/run_tests.go -mode unit
//   go run scripts/run_tests.go -mode integration
//   go run scripts/run_tests.go -mode benchmark
//   go run scripts/run_tests.go -mode race

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func main() {
	mode := flag.String("mode", "all", "test mode: unit, integration, benchmark, race, cover, all")
	logDir := flag.String("logdir", "logs", "directory for test logs")
	timeout := flag.Duration("timeout", 5*time.Minute, "go test timeout")
	flag.Parse()

	root := projectRoot()
	logs := filepath.Join(root, *logDir)
	must(os.MkdirAll(logs, 0o755))
	ts := time.Now().Format("2006-01-02T15-04-05.000")

	switch *mode {
	case "unit":
		must(runGoTest(root, logs, ts, "unit", *timeout, "./api", "./cmd/...", "./internal/..."))
	case "integration":
		must(runGoTest(root, logs, ts, "integration", *timeout, "./tests/..."))
	case "benchmark":
		must(runBenchmark(root, logs, ts, *timeout))
	case "race":
		must(runRace(root, logs, ts, *timeout))
	case "cover":
		must(runCover(root, logs, ts, *timeout))
	case "all":
		printBanner("shark-socket test suite")
		must(runGoTest(root, logs, ts, "unit", *timeout, "./api", "./cmd/...", "./internal/..."))
		must(runGoTest(root, logs, ts, "integration", *timeout, "./tests/..."))
		must(runBenchmark(root, logs, ts, *timeout))
		fmt.Printf("\nlogs: %s\n", logs)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", *mode)
		os.Exit(2)
	}
}

func runGoTest(root, logs, ts, mode string, timeout time.Duration, packages ...string) error {
	jsonFile := filepath.Join(logs, ts+"_"+mode+".json")
	logFile := filepath.Join(logs, ts+"_"+mode+".log")
	args := []string{"test", "-json", "-v", "-count=1", "-timeout=" + timeout.String()}
	args = append(args, packages...)
	fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05.000"), strings.Join(append([]string{"go"}, args...), " "), jsonFile)
	err := capture(root, jsonFile, "go", args...)
	writeReport(root, jsonFile, logFile)
	return err
}

func runBenchmark(root, logs, ts string, timeout time.Duration) error {
	jsonFile := filepath.Join(logs, ts+"_benchmark.json")
	logFile := filepath.Join(logs, ts+"_benchmark.log")
	args := []string{"test", "-json", "-run=^$", "-bench=.", "-benchmem", "-count=1", "-timeout=" + timeout.String(), "./internal/transport/tcp", "./internal/transport/coap", "./tests/benchmark"}
	fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05.000"), strings.Join(append([]string{"go"}, args...), " "), jsonFile)
	err := capture(root, jsonFile, "go", args...)
	writeReport(root, jsonFile, logFile)
	return err
}

func runRace(root, logs, ts string, timeout time.Duration) error {
	jsonFile := filepath.Join(logs, ts+"_race.json")
	logFile := filepath.Join(logs, ts+"_race.log")
	args := []string{"test", "-json", "-race", "-v", "-count=1", "-timeout=" + timeout.String(), "./..."}
	env := raceEnv()
	fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05.000"), strings.Join(append([]string{"go"}, args...), " "), jsonFile)
	err := captureEnv(root, jsonFile, env, "go", args...)
	writeReport(root, jsonFile, logFile)
	return err
}

func raceEnv() []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "CGO_ENABLED=1")
	if runtime.GOOS != "windows" {
		return env
	}
	paths := []string{}
	for _, path := range []string{`D:\Programs\w64devkit\bin`, `D:\Programs\LLVM\bin`} {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			paths = append(paths, path)
		}
	}
	if len(paths) > 0 {
		paths = append(paths, os.Getenv("PATH"))
		env = append(env, "PATH="+strings.Join(paths, string(os.PathListSeparator)))
	}
	return env
}

func runCover(root, logs, ts string, timeout time.Duration) error {
	logFile := filepath.Join(logs, ts+"_cover.log")
	coverFile := filepath.Join(logs, "coverage.out")
	args := []string{"test", "./...", "-count=1", "-coverprofile=" + coverFile, "-timeout=" + timeout.String()}
	fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05.000"), strings.Join(append([]string{"go"}, args...), " "), logFile)
	out, err := output(root, "go", args...)
	_ = os.WriteFile(logFile, out, 0o644)
	fmt.Print(string(out))
	if err != nil {
		return err
	}
	// Compute total coverage from the profile
	detailFile := filepath.Join(logs, ts+"_cover_detail.log")
	totalOut, covErr := output(root, "go", "tool", "cover", "-func="+coverFile)
	fmt.Print(string(totalOut))
	_ = os.WriteFile(detailFile, totalOut, 0o644)
	if covErr != nil {
		return covErr
	}
	total := parseCoverageTotal(string(totalOut))
	fmt.Printf("\nTotal coverage: %.1f%%\n", total)
	const minCoverage = 50.0
	if total < minCoverage {
		return fmt.Errorf("coverage %.1f%% is below minimum %.1f%%", total, minCoverage)
	}
	return nil
}

func parseCoverageTotal(output string) float64 {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return 0
	}
	lastLine := lines[len(lines)-1]
	// Last line is: "total:\t(statements)\tXX.X%"
	fields := strings.Fields(lastLine)
	for _, f := range fields {
		if strings.HasSuffix(f, "%") {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(f, "%"), 64)
			return v
		}
	}
	return 0
}

func writeReport(root, jsonFile, logFile string) {
	out, _ := output(root, "go", "run", "scripts/parse_test_log.go", jsonFile)
	_ = os.WriteFile(logFile, out, 0o644)
	fmt.Print(string(out))
	fmt.Printf("report: %s\n", logFile)
}

func projectRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		next := filepath.Dir(wd)
		if next == wd {
			panic("go.mod not found")
		}
		wd = next
	}
}

func capture(dir, file, name string, args ...string) error {
	return captureEnv(dir, file, os.Environ(), name, args...)
}

func captureEnv(dir, file string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(file, out, 0o644)
	return err
}

func output(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func printBanner(title string) {
	fmt.Println(strings.Repeat("=", 72))
	fmt.Println(" ", title)
	fmt.Println(" ", time.Now().Format("2006-01-02T15:04:05.000"))
	fmt.Println(strings.Repeat("=", 72))
}
