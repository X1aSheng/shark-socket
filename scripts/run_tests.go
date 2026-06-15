//go:build ignore

// shark-socket scripted test runner.
//
// Examples:
//   go run scripts/run_tests.go                          # unit + integration + benchmark
//   go run scripts/run_tests.go -mode unit               # unit tests only
//   go run scripts/run_tests.go -mode integration        # integration tests + deploy validation
//   go run scripts/run_tests.go -mode vet                # go vet (replaces validate.ps1)
//   go run scripts/run_tests.go -mode race               # race detector (replaces validate.ps1 -Race)
//   go run scripts/run_tests.go -mode cover              # coverage profile
//   go run scripts/run_tests.go -mode benchmark          # all benchmarks
//   go run scripts/run_tests.go -mode all                # unit + integration + benchmark

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
	mode := flag.String("mode", "all", "test mode: unit, integration, vet, race, cover, benchmark, all")
	logDir := flag.String("logdir", "logs", "directory for test logs")
	timeout := flag.Duration("timeout", 5*time.Minute, "go test timeout")
	jsonOut := flag.Bool("json", false, "output test results in JSON format (default: text log)")
	flag.Parse()

	root := projectRoot()
	logs := filepath.Join(root, *logDir)
	must(os.MkdirAll(logs, 0o755))
	ts := time.Now().Format("2006-01-02T15-04-05")

	switch *mode {
	case "unit":
		must(runGoTest(root, logs, ts, "unit", *timeout, *jsonOut, "./api", "./cmd/...", "./internal/..."))
	case "integration", "deploy":
		must(runGoTest(root, logs, ts, "integration", *timeout, *jsonOut, "./tests/..."))
	case "vet":
		must(runGoVet(root, logs, ts))
	case "benchmark":
		must(runBenchmark(root, logs, ts, *timeout, *jsonOut))
	case "race":
		must(runRace(root, logs, ts, *timeout, *jsonOut))
	case "cover":
		must(runCover(root, logs, ts, *timeout))
	case "all":
		printBanner("shark-socket test suite")
		must(runGoTest(root, logs, ts, "unit", *timeout, *jsonOut, "./api", "./cmd/...", "./internal/..."))
		must(runGoTest(root, logs, ts, "integration", *timeout, *jsonOut, "./tests/..."))
		must(runBenchmark(root, logs, ts, *timeout, *jsonOut))
		fmt.Printf("\nlogs: %s\n", logs)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", *mode)
		os.Exit(2)
	}
}

func runGoTest(root, logs, ts, mode string, timeout time.Duration, jsonMode bool, packages ...string) error {
	args := []string{"test", "-v", "-count=1", "-timeout=" + timeout.String()}
	if jsonMode {
		// Insert -json after "test" to form "go test -json ..."
		args = append(args[:1], append([]string{"-json"}, args[1:]...)...)
	}
	args = append(args, packages...)

	if jsonMode {
		jsonFile := filepath.Join(logs, ts+"_"+mode+".json")
		logFile := filepath.Join(logs, ts+"_"+mode+".log")
		fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05"), strings.Join(append([]string{"go"}, args...), " "), jsonFile)
		err := capture(root, jsonFile, "go", args...)
		writeReport(root, jsonFile, logFile)
		return err
	}

	// Text mode: output directly to .log file
	logFile := filepath.Join(logs, ts+"_"+mode+".log")
	fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05"), strings.Join(append([]string{"go"}, args...), " "), logFile)
	out, err := output(root, "go", args...)
	_ = os.WriteFile(logFile, out, 0o644)
	fmt.Print(string(out))
	return err
}

func runGoVet(root, logs, ts string) error {
	logFile := filepath.Join(logs, ts+"_vet.log")
	args := []string{"vet", "./..."}
	fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05"), strings.Join(append([]string{"go"}, args...), " "), logFile)
	out, err := output(root, "go", args...)
	_ = os.WriteFile(logFile, out, 0o644)
	fmt.Print(string(out))
	return err
}

func runBenchmark(root, logs, ts string, timeout time.Duration, jsonMode bool) error {
	if jsonMode {
		jsonFile := filepath.Join(logs, ts+"_benchmark.json")
		logFile := filepath.Join(logs, ts+"_benchmark.log")
		args := []string{"test", "-json", "-run=^$", "-bench=.", "-benchmem", "-count=1", "-timeout=" + timeout.String(), "./internal/transport/tcp", "./internal/transport/coap", "./tests/benchmark"}
		fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05"), strings.Join(append([]string{"go"}, args...), " "), jsonFile)
		err := capture(root, jsonFile, "go", args...)
		writeReport(root, jsonFile, logFile)
		return err
	}

	// Text mode
	logFile := filepath.Join(logs, ts+"_benchmark.log")
	args := []string{"test", "-v", "-run=^$", "-bench=.", "-benchmem", "-count=1", "-timeout=" + timeout.String(), "./internal/transport/tcp", "./internal/transport/coap", "./tests/benchmark"}
	fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05"), strings.Join(append([]string{"go"}, args...), " "), logFile)
	out, err := output(root, "go", args...)
	_ = os.WriteFile(logFile, out, 0o644)
	fmt.Print(string(out))
	return err
}

func runRace(root, logs, ts string, timeout time.Duration, jsonMode bool) error {
	env := raceEnv()

	if jsonMode {
		jsonFile := filepath.Join(logs, ts+"_race.json")
		logFile := filepath.Join(logs, ts+"_race.log")
		args := []string{"test", "-json", "-race", "-v", "-count=1", "-timeout=" + timeout.String(), "./..."}
		fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05"), strings.Join(append([]string{"go"}, args...), " "), jsonFile)
		err := captureEnv(root, jsonFile, env, "go", args...)
		writeReport(root, jsonFile, logFile)
		return err
	}

	// Text mode
	logFile := filepath.Join(logs, ts+"_race.log")
	args := []string{"test", "-race", "-v", "-count=1", "-timeout=" + timeout.String(), "./..."}
	fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05"), strings.Join(append([]string{"go"}, args...), " "), logFile)
	out, err := outputEnv(root, env, "go", args...)
	_ = os.WriteFile(logFile, out, 0o644)
	fmt.Print(string(out))
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
	fmt.Printf("[%s] %s -> %s\n", time.Now().Format("2006-01-02T15:04:05"), strings.Join(append([]string{"go"}, args...), " "), logFile)
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
	const minCoverage = 70.0
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

func outputEnv(dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
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
	fmt.Println(" ", time.Now().Format("2006-01-02T15:04:05"))
	fmt.Println(strings.Repeat("=", 72))
}
