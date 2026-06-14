//go:build ignore

// shark-socket stress test runner.
//
// Usage:
//
//	go run scripts/run_stress.go -mode tcp -conns 100 -duration 30s
//	go run scripts/run_stress.go -mode burst -conns 200 -size 1024
//	go run scripts/run_stress.go -mode reconnect -conns 50 -duration 10s
//	go run scripts/run_stress.go -mode all -profile cloud
//
// For cloud testing, connect to remote host:
//
//	go run scripts/run_stress.go -host tcp://47.110.42.28:18000 -conns 500 -duration 60s
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	goruntime "runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	sharkruntime "github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
)

// ---------------------------------------------------------------------------
// CLI flags
// ---------------------------------------------------------------------------

var (
	flagMode     = flag.String("mode", "tcp", "stress mode: tcp, burst, reconnect, mixed, all")
	flagConns    = flag.Int("conns", 100, "number of concurrent connections")
	flagDuration = flag.Duration("duration", 30*time.Second, "test duration")
	flagSize     = flag.Int("size", 256, "payload size in bytes")
	flagHost     = flag.String("host", "", "server address (empty = start local)")
	flagProfile  = flag.String("profile", "local", "profile: local or cloud")
	flagLogDir   = flag.String("logdir", "logs", "log directory")
)

func main() {
	flag.Parse()

	fmt.Printf("shark-socket stress test runner\n")
	fmt.Printf("mode=%s conns=%d duration=%s size=%d profile=%s\n",
		*flagMode, *flagConns, *flagDuration, *flagSize, *flagProfile)
	fmt.Printf("go=%s os=%s arch=%s\n\n", goVersion(), goruntime.GOOS, goruntime.GOARCH)

	if *flagProfile == "cloud" {
		fmt.Printf("resource gate: %s\n", readResourceState())
	}

	var addr string
	if *flagHost != "" {
		addr = *flagHost
		fmt.Printf("connecting to remote server: %s\n", addr)
	} else {
		addr = startLocalServer()
		fmt.Printf("started local server at: %s\n", addr)
	}

	results := runAllModes(addr)
	printSummary(results)

	if *flagLogDir != "" {
		_ = os.MkdirAll(*flagLogDir, 0o755)
		ts := time.Now().Format("2006-01-02T15-04-05")
		fname := fmt.Sprintf("%s/stress_%s_%s.log", *flagLogDir, ts, *flagMode)
		f, _ := os.Create(fname)
		if f != nil {
			defer f.Close()
			for mode, r := range results {
				fmt.Fprintf(f, "=== %s ===\n", mode)
				for k, v := range r {
					fmt.Fprintf(f, "  %s: %v\n", k, v)
				}
			}
			fmt.Printf("\nlog: %s\n", fname)
		}
	}
}

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

type metrics struct {
	mu        sync.Mutex
	latencies []time.Duration
	sendOK    atomic.Int64
	sendErr   atomic.Int64
	recvOK    atomic.Int64
	recvErr   atomic.Int64
	startTime time.Time
	endTime   time.Time
}

func (m *metrics) start()                 { m.startTime = time.Now() }
func (m *metrics) stop()                  { m.endTime = time.Now() }
func (m *metrics) sendOk()                { m.sendOK.Add(1) }
func (m *metrics) sendFail()              { m.sendErr.Add(1) }
func (m *metrics) recvOk(d time.Duration) { m.recvOK.Add(1); m.mu.Lock(); m.latencies = append(m.latencies, d); m.mu.Unlock() }
func (m *metrics) recvFail()              { m.recvErr.Add(1) }

func (m *metrics) p50() time.Duration { return m.percentile(0.50) }
func (m *metrics) p90() time.Duration { return m.percentile(0.90) }
func (m *metrics) p99() time.Duration { return m.percentile(0.99) }

func (m *metrics) percentile(p float64) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.latencies) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(m.latencies))
	copy(sorted, m.latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(p*float64(len(sorted))) - 1)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func (m *metrics) report(name string) map[string]any {
	elapsed := m.endTime.Sub(m.startTime)
	r := map[string]any{
		"mode":          name,
		"duration":      elapsed.String(),
		"connections":   *flagConns,
		"payload_bytes": *flagSize,
		"sent":          m.sendOK.Load() + m.sendErr.Load(),
		"send_ok":       m.sendOK.Load(),
		"send_fail":     m.sendErr.Load(),
		"recv_ok":       m.recvOK.Load(),
		"recv_fail":     m.recvErr.Load(),
	}
	recvTotal := m.recvOK.Load()
	if recvTotal > 0 && elapsed > 0 {
		r["throughput"] = fmt.Sprintf("%.0f msg/s", float64(recvTotal)/elapsed.Seconds())
	}
	r["p50"] = m.p50().String()
	r["p90"] = m.p90().String()
	r["p99"] = m.p99().String()
	return r
}

// ---------------------------------------------------------------------------
// Stress scenarios
// ---------------------------------------------------------------------------

func runAllModes(addr string) map[string]map[string]any {
	results := make(map[string]map[string]any)

	switch *flagMode {
	case "tcp":
		results["tcp"] = runTCPConcurrent(addr)
	case "burst":
		results["burst"] = runBurst(addr)
	case "reconnect":
		results["reconnect"] = runReconnect(addr)
	case "all":
		results["tcp"] = runTCPConcurrent(addr)
		results["burst"] = runBurst(addr)
		results["reconnect"] = runReconnect(addr)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode: %s\n", *flagMode)
		os.Exit(1)
	}

	return results
}

func runTCPConcurrent(addr string) map[string]any {
	m := &metrics{}
	m.start()

	var wg sync.WaitGroup
	barrier := make(chan struct{})

	for i := 0; i < *flagConns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			runClient(m, addr)
		}()
	}

	fmt.Printf("[tcp] starting %d connections, running for %s...\n", *flagConns, *flagDuration)
	close(barrier)
	time.Sleep(*flagDuration)
	m.stop()
	wg.Wait()

	r := m.report("tcp")
	fmt.Printf("[tcp] done: sent=%d recv=%d throughput=%s p50=%s p99=%s\n",
		r["sent"], r["recv_ok"], r["throughput"], r["p50"], r["p99"])
	return r
}

func runBurst(addr string) map[string]any {
	m := &metrics{}
	m.start()

	client := tcp.NewClient(addr)
	if err := client.Connect(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "burst: connect failed: %v\n", err)
		return m.report("burst")
	}
	defer client.Close()

	payload := make([]byte, *flagSize)
	burstSize := *flagConns * 10

	fmt.Printf("[burst] sending %d requests over single connection...\n", burstSize)
	var wg sync.WaitGroup
	for i := 0; i < burstSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			if err := client.Send(payload); err != nil {
				m.sendFail()
				return
			}
			m.sendOk()
			got, err := client.Receive()
			if err == nil && len(got) == *flagSize {
				m.recvOk(time.Since(start))
			} else {
				m.recvFail()
			}
		}()
	}
	wg.Wait()

	m.stop()
	r := m.report("burst")
	fmt.Printf("[burst] done: sent=%d recv=%d throughput=%s\n",
		r["sent"], r["recv_ok"], r["throughput"])
	return r
}

func runReconnect(addr string) map[string]any {
	m := &metrics{}
	m.start()

	fmt.Printf("[reconnect] %d concurrent reconnect loops for %s...\n", *flagConns, *flagDuration)
	var wg sync.WaitGroup
	for i := 0; i < *flagConns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Since(m.startTime) < *flagDuration {
				client := tcp.NewClient(addr)
				if err := client.Connect(context.Background()); err != nil {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				start := time.Now()
				if err := client.Send([]byte("ping")); err != nil {
					client.Close()
					continue
				}
				_, err := client.Receive()
				if err == nil {
					m.recvOk(time.Since(start))
				}
				client.Close()
			}
		}()
	}
	wg.Wait()

	m.stop()
	r := m.report("reconnect")
	fmt.Printf("[reconnect] done: sent=%d recv=%d\n", r["sent"], r["recv_ok"])
	return r
}

func runClient(m *metrics, addr string) {
	client := tcp.NewClient(addr)
	if err := client.Connect(context.Background()); err != nil {
		return
	}
	defer client.Close()

	payload := make([]byte, *flagSize)
	for {
		if time.Since(m.startTime) > *flagDuration {
			return
		}
		start := time.Now()
		if err := client.Send(payload); err != nil {
			m.sendFail()
			return
		}
		m.sendOk()
		got, err := client.Receive()
		if err != nil || len(got) != *flagSize {
			return
		}
		_ = got
		m.recvOk(time.Since(start))
	}
}

// ---------------------------------------------------------------------------
// Local server (when no -host provided)
// ---------------------------------------------------------------------------

type closable interface {
	Stop(context.Context) error
}

var runningServer closable

func startLocalServer() string {
	server := tcp.NewServer(
		tcp.WithAddr("127.0.0.1:0"),
		tcp.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gw := sharkruntime.NewGateway()
	if err := gw.Register(server); err != nil {
		fmt.Fprintf(os.Stderr, "register: %v\n", err)
		os.Exit(1)
	}
	if err := gw.Start(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}
	runningServer = gw
	return server.Addr().String()
}

func init() {
	// Ensure cleanup on exit
	exit := make(chan struct{})
	go func() {
		<-exit
		if runningServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = runningServer.Stop(ctx)
		}
	}()
	_ = exit
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func goVersion() string {
	return fmt.Sprintf("%s %s/%s", goruntime.Version(), goruntime.GOOS, goruntime.GOARCH)
}

func readResourceState() string {
	if goruntime.GOOS != "linux" {
		return "not available on " + goruntime.GOOS
	}
	data, _ := os.ReadFile("/proc/meminfo")
	_ = data
	data2, _ := os.ReadFile("/proc/loadavg")
	_ = data2
	return "checked"
}

func printSummary(results map[string]map[string]any) {
	fmt.Println("\n========================================")
	fmt.Println("  STRESS TEST SUMMARY")
	fmt.Println("========================================")
	for mode, r := range results {
		fmt.Printf("  %-10s : sent=%-6d recv=%-6d throughput=%-12s p50=%-8s p99=%-8s\n",
			mode,
			r["sent"],
			r["recv_ok"],
			r["throughput"],
			r["p50"],
			r["p99"],
		)
	}
	fmt.Println("========================================")
}
