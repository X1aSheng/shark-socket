package http

import (
	"bufio"
	"context"
	"net"
	stdhttp "net/http"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// countingMetrics records counter names so the tests can assert that a refusal
// was reported, not only that it happened.
type countingMetrics struct {
	mu     sync.Mutex
	counts map[string]int
}

func newCountingMetrics() *countingMetrics { return &countingMetrics{counts: map[string]int{}} }

func (m *countingMetrics) IncCounter(name string, _ ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[name]++
}

func (m *countingMetrics) SetGauge(string, float64, ...string)         {}
func (m *countingMetrics) ObserveHistogram(string, float64, ...string) {}

func (m *countingMetrics) get(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[name]
}

// observableRuntime substitutes a metrics sink; the runtime's own metrics
// option is unexported.
type observableRuntime struct {
	*runtime.Runtime
	metrics core.Metrics
}

func (r observableRuntime) Metrics() core.Metrics { return r.metrics }

func newObservableRuntime(m core.Metrics) core.Runtime {
	return observableRuntime{Runtime: runtime.NewRuntime(nil, nil), metrics: m}
}

// gatedServer is a server whose handler blocks until release is called, so a
// test can hold a connection open with certainty instead of with sleeps.
type gatedServer struct {
	server  *Server
	metrics *countingMetrics
	entered chan struct{}
	release func()
}

func startGatedServer(t *testing.T, opts ...Option) *gatedServer {
	t.Helper()
	releaseCh := make(chan struct{})
	var once sync.Once
	g := &gatedServer{
		metrics: newCountingMetrics(),
		entered: make(chan struct{}, 16),
		release: func() { once.Do(func() { close(releaseCh) }) },
	}
	opts = append(opts, WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			g.entered <- struct{}{}
			<-releaseCh
			return sess.Send([]byte("ok"))
		}))
	g.server = NewServer(opts...)
	g.server.rt = newObservableRuntime(g.metrics)
	if err := g.server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		g.release()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = g.server.Stop(ctx)
	})
	return g
}

func (g *gatedServer) get(t *testing.T) *bufio.Reader {
	t.Helper()
	conn, err := net.Dial("tcp", g.server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: test\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	return bufio.NewReader(conn)
}

func (g *gatedServer) awaitHandler(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request never reached the handler")
	}
}

func readStatus(t *testing.T, r *bufio.Reader) (int, func()) {
	t.Helper()
	resp, err := stdhttp.ReadResponse(r, nil)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	return resp.StatusCode, func() { resp.Body.Close() }
}

// TestHTTPMaxConnectionsAnswers503 is A3: the plain HTTP transport had no
// connection ceiling at all, while every other transport in the gateway did.
func TestHTTPMaxConnectionsAnswers503(t *testing.T) {
	g := startGatedServer(t, WithMaxConnections(1))

	firstReader := g.get(t)
	g.awaitHandler(t)

	secondReader := g.get(t)
	status, closeBody := readStatus(t, secondReader)
	defer closeBody()
	if status != stdhttp.StatusServiceUnavailable {
		t.Fatalf("second connection status = %d, want 503", status)
	}
	if got := g.metrics.get("sessions_rejected_total"); got != 1 {
		t.Errorf("sessions_rejected_total = %d, want 1", got)
	}

	// The first connection was not collateral damage.
	g.release()
	if status, closeBody := readStatus(t, firstReader); status != stdhttp.StatusOK {
		closeBody()
		t.Fatalf("first connection status = %d, want 200", status)
	} else {
		closeBody()
	}
}

// TestHTTPMaxConnectionsPerIPAnswers503 is A5 on the HTTP transport: the global
// cap still has room, so only the per-peer rule can be the reason for the 503.
func TestHTTPMaxConnectionsPerIPAnswers503(t *testing.T) {
	g := startGatedServer(t, WithMaxConnections(10), WithMaxConnectionsPerIP(1))

	_ = g.get(t)
	g.awaitHandler(t)

	secondReader := g.get(t)
	status, closeBody := readStatus(t, secondReader)
	defer closeBody()
	if status != stdhttp.StatusServiceUnavailable {
		t.Fatalf("second connection status = %d, want 503 (nine global slots were free)", status)
	}
	if got := g.metrics.get("sessions_rejected_total"); got != 1 {
		t.Errorf("sessions_rejected_total = %d, want 1", got)
	}
}

// TestHTTPUnlimitedKeepsServing guards the default: with no limits configured
// the gate listener must not be installed at all.
func TestHTTPUnlimitedKeepsServing(t *testing.T) {
	g := startGatedServer(t)

	readers := make([]*bufio.Reader, 5)
	for i := range readers {
		readers[i] = g.get(t)
	}
	g.release()
	for i, r := range readers {
		status, closeBody := readStatus(t, r)
		closeBody()
		if status != stdhttp.StatusOK {
			t.Fatalf("connection %d status = %d, want 200", i, status)
		}
	}
}
