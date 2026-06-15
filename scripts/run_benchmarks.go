//go:build ignore

// shark-socket resource-aware benchmark runner.
//
// Examples:
//   go run scripts/run_benchmarks.go -profile local -stage smoke
//   go run scripts/run_benchmarks.go -profile cloud -stage light
//   go run scripts/run_benchmarks.go -profile cloud -stage medium -logdir logs/cloud

package main

import (
	"bufio"
	"bytes"
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

type benchmarkGroup struct {
	name      string
	pattern   string
	packages  []string
	benchtime time.Duration
	timeout   time.Duration
	cloud     bool
	medium    bool
}

type resourceState struct {
	memAvailableMB int
	load1          float64
	ok             bool
}

func main() {
	profile := flag.String("profile", "local", "benchmark profile: local or cloud")
	stage := flag.String("stage", "smoke", "benchmark stage: smoke, light, or medium")
	logDir := flag.String("logdir", "logs", "directory for benchmark logs")
	listGroups := flag.Bool("list", false, "list all benchmark groups and exit")
	benchFilter := flag.String("bench", "", "run only the named benchmark group (substring match)")
	flag.Parse()

	if *profile != "local" && *profile != "cloud" {
		exitf("unknown profile %q", *profile)
	}
	if *stage != "smoke" && *stage != "light" && *stage != "medium" {
		exitf("unknown stage %q", *stage)
	}

	if *listGroups {
		groups := allBenchmarkGroups()
		if *benchFilter != "" {
			lower := strings.ToLower(*benchFilter)
			out := groups[:0]
			for _, g := range groups {
				if strings.Contains(strings.ToLower(g.name), lower) {
					out = append(out, g)
				}
			}
			groups = out
		}
		for _, g := range groups {
			fmt.Printf("  %-30s  pattern=%-50s  benchtime=%-10s  cloud=%v  medium=%v\n",
				g.name, g.pattern, g.benchtime, g.cloud, g.medium)
		}
		return
	}

	root := projectRoot()
	logs := filepath.Join(root, *logDir)
	must(os.MkdirAll(logs, 0o755))
	ts := time.Now().Format("2006-01-02T15-04-05")

	fmt.Printf("shark-socket benchmark matrix\n")
	fmt.Printf("profile=%s stage=%s root=%s\n", *profile, *stage, root)
	fmt.Printf("go=%s os=%s arch=%s\n\n", goVersion(root), runtime.GOOS, runtime.GOARCH)

	for _, group := range selectGroups(*profile, *stage, *benchFilter) {
		if *profile == "cloud" {
			state := readResourceState()
			fmt.Printf("[%s] resource before %s: %s\n", time.Now().Format(time.RFC3339), group.name, state)
			if !resourceGate(state, group.medium) {
				fmt.Printf("SKIP %s: resource gate did not pass\n", group.name)
				if group.medium {
					break
				}
				continue
			}
		}
		if err := runGroup(root, logs, ts, group); err != nil {
			exitf("benchmark group %s failed: %v", group.name, err)
		}
		if *profile == "cloud" {
			fmt.Printf("[%s] resource after %s: %s\n", time.Now().Format(time.RFC3339), group.name, readResourceState())
		}
	}
}


// allBenchmarkGroups returns the complete benchmark group registry.
func allBenchmarkGroups() []benchmarkGroup {
	return []benchmarkGroup{
		{
			name:      "core-smoke",
			pattern:   "BenchmarkSessionManager|BenchmarkPluginChain",
			packages:  []string{"./tests/benchmark"},
			benchtime: 100 * time.Millisecond,
			timeout:   120 * time.Second,
			cloud:     true,
		},
		{
			name:      "coap-smoke",
			pattern:   "BenchmarkMessageParse|BenchmarkMessageMarshal",
			packages:  []string{"./internal/transport/coap"},
			benchtime: 100 * time.Millisecond,
			timeout:   120 * time.Second,
			cloud:     true,
		},
		{
			name:      "tcp-udp-light",
			pattern:   "BenchmarkTCPEcho$|BenchmarkUDPEcho$",
			packages:  []string{"./tests/benchmark"},
			benchtime: 300 * time.Millisecond,
			timeout:   180 * time.Second,
			cloud:     true,
		},
		{
			name:      "http-ws-light",
			pattern:   "BenchmarkHTTPEcho$|BenchmarkWSEcho$",
			packages:  []string{"./tests/benchmark"},
			benchtime: 300 * time.Millisecond,
			timeout:   180 * time.Second,
			cloud:     true,
		},
		{
			name:      "core-medium",
			pattern:   "BenchmarkSessionManager|BenchmarkPluginChain|BenchmarkMessageParse|BenchmarkMessageMarshal",
			packages:  []string{"./tests/benchmark", "./internal/transport/coap"},
			benchtime: time.Second,
			timeout:   180 * time.Second,
			cloud:     true,
			medium:    true,
		},
		// --- New payload-size benchmarks ---
		{
			name:      "payload-size-light",
			pattern:   "BenchmarkTCPEcho_PayloadSize|BenchmarkUDPEcho_PayloadSize|BenchmarkWSEcho_PayloadSize",
			packages:  []string{"./tests/benchmark"},
			benchtime: 200 * time.Millisecond,
			timeout:   240 * time.Second,
			cloud:     true,
		},
		{
			name:      "payload-size-http-grpc-light",
			pattern:   "BenchmarkHTTPEcho_PayloadSize|BenchmarkGRPCWebEcho_PayloadSize",
			packages:  []string{"./tests/benchmark"},
			benchtime: 200 * time.Millisecond,
			timeout:   240 * time.Second,
			cloud:     true,
		},
		{
			name:      "payload-size-medium",
			pattern:   "BenchmarkQUICEcho_PayloadSize",
			packages:  []string{"./tests/benchmark"},
			benchtime: 100 * time.Millisecond,
			timeout:   300 * time.Second,
			cloud:     true,
			medium:    true,
		},
		// --- New concurrent benchmarks ---
		{
			name:      "concurrent-tcp-udp-light",
			pattern:   "BenchmarkTCPEcho_Concurrent|BenchmarkUDPEcho_Concurrent",
			packages:  []string{"./tests/benchmark"},
			benchtime: 200 * time.Millisecond,
			timeout:   240 * time.Second,
			cloud:     true,
		},
		{
			name:      "concurrent-ws-http-light",
			pattern:   "BenchmarkWSEcho_Concurrent|BenchmarkHTTPEcho_Concurrent",
			packages:  []string{"./tests/benchmark"},
			benchtime: 200 * time.Millisecond,
			timeout:   240 * time.Second,
			cloud:     true,
		},
		{
			name:      "concurrent-grpc-medium",
			pattern:   "BenchmarkGRPCWebEcho_Concurrent",
			packages:  []string{"./tests/benchmark"},
			benchtime: 200 * time.Millisecond,
			timeout:   240 * time.Second,
			cloud:     true,
			medium:    true,
		},
		// --- New plugin benchmarks ---
		{
			name:      "plugins-light",
			pattern:   "BenchmarkPluginChain_Blacklist$|BenchmarkPluginChain_RateLimit$",
			packages:  []string{"./tests/benchmark"},
			benchtime: 200 * time.Millisecond,
			timeout:   120 * time.Second,
			cloud:     true,
		},
		{
			name:      "plugins-medium",
			pattern:   "BenchmarkPluginChain_BlacklistRateLimit|BenchmarkPluginChain_FullChain",
			packages:  []string{"./tests/benchmark"},
			benchtime: time.Second,
			timeout:   180 * time.Second,
			cloud:     true,
			medium:    true,
		},
	}

}


func selectGroups(profile, stage, benchFilter string) []benchmarkGroup {
	if benchFilter != "" {
		lower := strings.ToLower(benchFilter)
		for _, g := range allBenchmarkGroups() {
			if strings.Contains(strings.ToLower(g.name), lower) {
				fmt.Printf("bench filter %q matched group %q (pattern=%s)\n", benchFilter, g.name, g.pattern)
				return []benchmarkGroup{g}
			}
		}
		exitf("no benchmark group matches %q; use -list to see available groups", benchFilter)
	}

	groups := allBenchmarkGroups()
	limit := 5
	if stage == "light" {
		limit = 10
	}
	if stage == "medium" {
		limit = len(groups)
	}
	selected := groups[:limit]
	if profile == "cloud" {
		out := selected[:0]
		for _, group := range selected {
			if group.cloud {
				out = append(out, group)
			}
		}
		return out
	}
	return selected
}

func runGroup(root, logs, ts string, group benchmarkGroup) error {
	logFile := filepath.Join(logs, ts+"_bench_"+group.name+".log")
	args := []string{
		"test",
		"-run=^$",
		"-bench=" + group.pattern,
		"-benchmem",
		"-benchtime=" + group.benchtime.String(),
		"-count=1",
		"-timeout=" + group.timeout.String(),
	}
	args = append(args, group.packages...)
	fmt.Printf("[%s] go %s\n", time.Now().Format(time.RFC3339), strings.Join(args, " "))
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(logFile, out, 0o644)
	fmt.Print(string(out))
	fmt.Printf("log: %s\n\n", logFile)
	return err
}

func resourceGate(state resourceState, medium bool) bool {
	if !state.ok {
		return runtime.GOOS != "linux"
	}
	if medium {
		return state.memAvailableMB >= 1024 && state.load1 <= 2.0
	}
	return state.memAvailableMB >= 768 && state.load1 <= 2.5
}

func readResourceState() resourceState {
	if runtime.GOOS != "linux" {
		return resourceState{ok: false}
	}
	mem, memOK := readMemAvailableMB()
	load, loadOK := readLoad1()
	return resourceState{memAvailableMB: mem, load1: load, ok: memOK && loadOK}
}

func readMemAvailableMB() (int, bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, err := strconv.Atoi(fields[1])
			if err != nil {
				return 0, false
			}
			return kb / 1024, true
		}
	}
	return 0, false
}

func readLoad1() (float64, bool) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, false
	}
	load, err := strconv.ParseFloat(fields[0], 64)
	return load, err == nil
}

func (s resourceState) String() string {
	if !s.ok {
		return "unavailable"
	}
	return fmt.Sprintf("MemAvailable=%dMB Load1=%.2f", s.memAvailableMB, s.load1)
}

func goVersion(root string) string {
	cmd := exec.Command("go", "version")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
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

func must(err error) {
	if err != nil {
		exitf("%v", err)
	}
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
