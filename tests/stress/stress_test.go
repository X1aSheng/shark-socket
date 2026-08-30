// Package stress provides load and stress tests for the shark-socket server.
//
// Run:
//
//	go test ./tests/stress/ -v -count=1 -timeout 120s
//	go test ./tests/stress/ -v -run TestStressTCPConnections -args -conns=100 -duration=15s
package stress

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	transporthttp "github.com/X1aSheng/shark-socket/internal/transport/http"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
	"github.com/X1aSheng/shark-socket/internal/transport/udp"
	"github.com/X1aSheng/shark-socket/internal/transport/websocket"
	gws "github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Config (set via TestMain or defaults)
// ---------------------------------------------------------------------------

var (
	conns       = 50
	duration    = 10 * time.Second
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

func (m *stressMetrics) start()       { m.startTime = time.Now() }
func (m *stressMetrics) stop()        { m.endTime = time.Now() }
func (m *stressMetrics) incSendOK()   { m.sendOK.Add(1) }
func (m *stressMetrics) incSendFail() { m.sendFail.Add(1) }
func (m *stressMetrics) incRecvOK(d time.Duration) {
	m.recvOK.Add(1)
	m.mu.Lock()
	m.latencies = append(m.latencies, d)
	m.mu.Unlock()
}
func (m *stressMetrics) incRecvFail() { m.recvFail.Add(1) }

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

	// One dedicated client per goroutine: a shared connection with concurrent
	// Send/Receive cannot attribute echoes to their sender, making the
	// throughput/latency numbers meaningless (the same fix applied to
	// run_stress.go runBurst). Linger(0) avoids TIME_WAIT accumulation; a
	// transient connect failure retries inside connectStressClient.
	payload := make([]byte, payloadSize)
	burstCount := conns * 10

	var wg sync.WaitGroup
	for i := 0; i < burstCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := connectStressClient(t, addr)
			defer client.Close()
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
				client := newStressClient(addr)
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

// newStressClient returns a TCP client with SO_LINGER=0. The reconnect test
// opens tens of thousands of short-lived connections; without linger 0 the
// client side accumulates TIME_WAIT sockets and exhausts the Windows ephemeral
// port range (WSAEADDRINUSE) partway through the run.
func newStressClient(addr string) *tcp.Client {
	return tcp.NewClient(addr, tcp.WithClientLinger(0))
}

// connectStressClient connects a stress client, retrying transient connect
// failures (e.g. ephemeral-port exhaustion) up to 10 times before failing.
func connectStressClient(t testing.TB, addr string) *tcp.Client {
	t.Helper()
	var lastErr error
	for i := 0; i < 10; i++ {
		client := newStressClient(addr)
		if err := client.Connect(context.Background()); err == nil {
			return client
		} else {
			lastErr = err
			client.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("connect stress client: %v", lastErr)
	return nil
}

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
	client := newStressClient(addr)
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

// ---------------------------------------------------------------------------
// Test: UDP sustained throughput + pseudo-session leak detection
// ---------------------------------------------------------------------------

func TestStressUDPConnections(t *testing.T) {
	addr, server := startStressUDPServer(t, 2*time.Second)
	m := &stressMetrics{}
	m.start()

	payload := make([]byte, payloadSize)
	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("udp", addr)
			if err != nil {
				m.incSendFail()
				return
			}
			defer conn.Close()
			buf := make([]byte, 2048)
			for {
				if time.Since(m.startTime) > duration {
					return
				}
				if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
					m.incSendFail()
					return
				}
				if _, err := conn.Write(payload); err != nil {
					m.incSendFail()
					return
				}
				m.incSendOK()
				if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					m.incRecvFail()
					return
				}
				start := time.Now()
				n, err := conn.Read(buf)
				if err != nil || n != payloadSize {
					m.incRecvFail()
					return
				}
				m.incRecvOK(time.Since(start))
			}
		}()
	}
	wg.Wait()
	m.stop()
	m.report("UDPConnections")

	if m.sendOK.Load() == 0 || m.recvOK.Load() == 0 {
		t.Fatal("no traffic flowed")
	}

	// Leak detection: after every peer stops, the TTL sweep must reclaim all
	// pseudo-sessions.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if server.SessionCount() == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("UDP pseudo-sessions leaked after peers stopped: count=%d", server.SessionCount())
}

// ---------------------------------------------------------------------------
// Test: WebSocket connection churn + session leak detection
// ---------------------------------------------------------------------------

func TestStressWSChurn(t *testing.T) {
	addr, gw := startStressWSServer(t)
	m := &stressMetrics{}
	m.start()

	// Linger(0) dialer: the churn test opens tens of thousands of short-lived
	// TCP connections, which would otherwise exhaust the Windows ephemeral
	// port range via TIME_WAIT.
	dialer := gws.Dialer{
		HandshakeTimeout: 2 * time.Second,
		NetDial: func(network, addr string) (net.Conn, error) {
			conn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				_ = tcpConn.SetLinger(0)
			}
			return conn, nil
		},
	}
	u := url.URL{Scheme: "ws", Host: addr, Path: "/ws"}

	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if time.Since(m.startTime) > duration {
					return
				}
				conn, _, err := dialer.Dial(u.String(), nil)
				if err != nil {
					continue
				}
				start := time.Now()
				if err := conn.WriteMessage(gws.BinaryMessage, []byte("ping")); err != nil {
					_ = conn.Close()
					m.incSendFail()
					continue
				}
				m.incSendOK()
				if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					_ = conn.Close()
					m.incRecvFail()
					continue
				}
				_, got, err := conn.ReadMessage()
				_ = conn.Close()
				if err == nil && string(got) == "ping" {
					m.incRecvOK(time.Since(start))
				} else {
					m.incRecvFail()
				}
			}
		}()
	}
	wg.Wait()
	m.stop()
	m.report("WSChurn")

	if m.recvOK.Load() == 0 {
		t.Fatal("no traffic flowed")
	}

	// Leak detection: every churned session must be reclaimed after its
	// connection closes.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if gw.Runtime().Sessions().Count() == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("WebSocket sessions leaked after churn: count=%d", gw.Runtime().Sessions().Count())
}

// ---------------------------------------------------------------------------
// Test: HTTP concurrent requests + session leak detection
// ---------------------------------------------------------------------------

func TestStressHTTPRequests(t *testing.T) {
	addr, gw := startStressHTTPServer(t)
	m := &stressMetrics{}
	m.start()

	// Linger(0) transport: per-request HTTP sessions churn short-lived TCP
	// connections and would exhaust the Windows ephemeral port range.
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 100,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				d := net.Dialer{}
				conn, err := d.DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				if tcpConn, ok := conn.(*net.TCPConn); ok {
					_ = tcpConn.SetLinger(0)
				}
				return conn, nil
			},
		},
	}
	payload := make([]byte, payloadSize)

	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if time.Since(m.startTime) > duration {
					return
				}
				start := time.Now()
				resp, err := client.Post("http://"+addr+"/", "application/octet-stream", bytes.NewReader(payload))
				if err != nil {
					m.incSendFail()
					continue
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil || len(body) != payloadSize {
					m.incRecvFail()
					continue
				}
				m.incSendOK()
				m.incRecvOK(time.Since(start))
			}
		}()
	}
	wg.Wait()
	m.stop()
	m.report("HTTPRequests")

	if m.recvOK.Load() == 0 {
		t.Fatal("no traffic flowed")
	}

	// Leak detection: HTTP Mode B sessions are per-request and must not
	// accumulate.
	if count := gw.Runtime().Sessions().Count(); count != 0 {
		t.Fatalf("HTTP sessions leaked: count=%d", count)
	}
}

// ---------------------------------------------------------------------------
// Non-TCP stress server helpers
// ---------------------------------------------------------------------------

func startStressUDPServer(t testing.TB, ttl time.Duration) (string, *udp.Server) {
	t.Helper()
	server := udp.NewServer(
		udp.WithAddr("127.0.0.1:0"),
		udp.WithSessionTTL(ttl),
		udp.WithSweepInterval(500*time.Millisecond),
		udp.WithHandler(func(sess core.Session, msg core.Message) error {
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
	return server.Addr().String(), server
}

func startStressWSServer(t testing.TB) (string, *runtime.Gateway) {
	t.Helper()
	server := websocket.NewServer(
		websocket.WithAddr("127.0.0.1:0"),
		websocket.WithPath("/ws"),
		websocket.WithCheckOrigin(func(*http.Request) bool { return true }),
		websocket.WithHandler(func(sess core.Session, msg core.Message) error {
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
	return server.Addr().String(), gw
}

func startStressHTTPServer(t testing.TB) (string, *runtime.Gateway) {
	t.Helper()
	server := newStressHTTPEchoServer()
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
	return server.Addr().String(), gw
}

// newStressHTTPEchoServer builds the HTTP echo server for the stress test.
func newStressHTTPEchoServer() *transporthttp.Server {
	return transporthttp.NewServer(
		transporthttp.WithAddr("127.0.0.1:0"),
		transporthttp.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
}
