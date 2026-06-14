// Package stress provides load and stress tests for the shark-socket server.
//
// Run:
//
//	go test ./tests/stress/ -v -count=1 -timeout 120s
//	go test ./tests/stress/ -v -run TestStressTCPConnections -args -conns=100 -duration=15s
package stress

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
)

// ---------------------------------------------------------------------------
// Config (set via TestMain or defaults)
// ---------------------------------------------------------------------------

var (
	conns    = 50
	duration = 10 * time.Second
	payloadSize = 256
)

func init() {
	// Exposed for TestMain flag parsing if needed
}

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

type stressMetrics struct {
	mu        sync.Mutex
	latencies []time.Duration
	sendOK    atomic.Int64
	sendFail  atomic.Int64
	recvOK    atomic.Int64
	recvFail  atomic.Int64
	startTime time.Time
	endTime   time.Time
}

func (m *stressMetrics) start()             { m.startTime = time.Now() }
func (m *stressMetrics) stop()              { m.endTime = time.Now() }
func (m *stressMetrics) incSendOK()            { m.sendOK.Add(1) }
func (m *stressMetrics) incSendFail()          { m.sendFail.Add(1) }
func (m *stressMetrics) incRecvOK(d time.Duration) { m.recvOK.Add(1); m.mu.Lock(); m.latencies = append(m.latencies, d); m.mu.Unlock() }
func (m *stressMetrics) incRecvFail()          { m.recvFail.Add(1) }

func (m *stressMetrics) percentile(p float64) time.Duration {
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

func (m *stressMetrics) report(name string) map[string]any {
	elapsed := m.endTime.Sub(m.startTime)
	r := map[string]any{
		"test":          name,
		"duration":      elapsed.String(),
		"connections":   conns,
		"payload_bytes": payloadSize,
		"sent":          m.sendOK.Load() + m.sendFail.Load(),
		"send_ok":       m.sendOK.Load(),
		"send_fail":     m.sendFail.Load(),
		"recv_ok":       m.recvOK.Load(),
		"recv_fail":     m.recvFail.Load(),
	}
	recvTotal := m.recvOK.Load()
	if recvTotal > 0 && elapsed > 0 {
		r["throughput"] = fmt.Sprintf("%.1f msg/s", float64(recvTotal)/elapsed.Seconds())
	}
	r["p50"] = m.percentile(0.50).String()
	r["p90"] = m.percentile(0.90).String()
	r["p99"] = m.percentile(0.99).String()

	// Print immediately
	fmt.Printf("\n=== Stress Report: %s ===\n", name)
	fmt.Printf("  Duration:    %s\n", r["duration"])
	fmt.Printf("  Connections: %d\n", conns)
	fmt.Printf("  Payload:     %d bytes\n", payloadSize)
	fmt.Printf("  Sent:        %d (ok=%d fail=%d)\n", r["sent"], m.sendOK.Load(), m.sendFail.Load())
	fmt.Printf("  Received:    %d (ok=%d fail=%d)\n", recvTotal+m.recvFail.Load(), m.recvOK.Load(), m.recvFail.Load())
	if tp, ok := r["throughput"]; ok {
		fmt.Printf("  Throughput:  %s\n", tp)
	}
	fmt.Printf("  P50/P90/P99: %s / %s / %s\n", r["p50"], r["p90"], r["p99"])
	fmt.Println()
	return r
}

// ---------------------------------------------------------------------------
// Test: TCP sustained concurrent connections
// ---------------------------------------------------------------------------

func TestStressTCPConnections(t *testing.T) {
	addr := startStressTCPServer(t)
	m := &stressMetrics{}
	m.start()

	var wg sync.WaitGroup
	barrier := make(chan struct{})

	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			runStressTCP(m, addr)
		}()
	}

	close(barrier)
	time.Sleep(duration)
	m.stop()
	wg.Wait()

	m.report("TCPConnections")
}

// ---------------------------------------------------------------------------
// Test: Burst traffic (single connection, many concurrent requests)
// ---------------------------------------------------------------------------

func TestStressTCPBurst(t *testing.T) {
	addr := startStressTCPServer(t)
	m := &stressMetrics{}
	m.start()

	client := tcp.NewClient(addr)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	payload := make([]byte, payloadSize)
	burstCount := conns * 10

	var wg sync.WaitGroup
	for i := 0; i < burstCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			if err := client.Send(payload); err != nil {
				m.incSendFail()
				return
			}
			m.incSendOK()
			got, err := client.Receive()
			if err == nil && len(got) == payloadSize {
				m.incRecvOK(time.Since(start))
			} else {
				m.incRecvFail()
			}
		}()
	}
	wg.Wait()

	m.stop()
	m.report("TCPBurst")
}

// ---------------------------------------------------------------------------
// Test: Connection churn (rapid connect/send/close)
// ---------------------------------------------------------------------------

func TestStressTCPReconnect(t *testing.T) {
	addr := startStressTCPServer(t)
	m := &stressMetrics{}
	m.start()

	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if time.Since(m.startTime) > duration {
					return
				}
				client := tcp.NewClient(addr)
				if err := client.Connect(context.Background()); err != nil {
					continue
				}
				start := time.Now()
				if err := client.Send([]byte("ping")); err != nil {
					client.Close()
					continue
				}
				_, err := client.Receive()
				if err == nil {
					m.incRecvOK(time.Since(start))
				}
				client.Close()
			}
		}()
	}
	wg.Wait()

	m.stop()
	m.report("TCPReconnect")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func startStressTCPServer(t testing.TB) string {
	t.Helper()
	server := tcp.NewServer(
		tcp.WithAddr("127.0.0.1:0"),
		tcp.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gw := runtime.NewGateway()
	if err := gw.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = gw.Stop(ctx)
	})
	return server.Addr().String()
}

func runStressTCP(m *stressMetrics, addr string) {
	client := tcp.NewClient(addr)
	if err := client.Connect(context.Background()); err != nil {
		return
	}
	defer client.Close()

	payload := make([]byte, payloadSize)
	for {
		if time.Since(m.startTime) > duration {
			return
		}
		start := time.Now()
		if err := client.Send(payload); err != nil {
			m.incSendFail()
			return
		}
		m.incSendOK()
		got, err := client.Receive()
		if err != nil || len(got) != payloadSize {
			m.incRecvFail()
			return
		}
		_ = got
		m.incRecvOK(time.Since(start))
	}
}
