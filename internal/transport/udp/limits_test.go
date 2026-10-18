package udp

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// countingMetrics records counter names so the tests can assert that a refusal
// was reported.
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

func startUDPServer(t *testing.T, metrics core.Metrics, opts ...Option) *Server {
	t.Helper()
	server := NewServer(opts...)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = gateway.Stop(ctx)
	})
	server.rt = observableRuntime{Runtime: runtime.NewRuntime(nil, nil), metrics: metrics}
	return server
}

// sendFrom emits one datagram from a chosen loopback address, so a test can act
// as several distinct peers without forging anything.
func sendFrom(t *testing.T, ip, serverAddr string) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(ip)})
	if err != nil {
		t.Fatalf("bind %s: %v", ip, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	raddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteToUDP([]byte("hello"), raddr); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestUDPMaxSessionsPerPeer is A1: without a per-peer dimension a single source
// fills the table, and with a forged source a single host fills it even faster.
func TestUDPMaxSessionsPerPeer(t *testing.T) {
	metrics := newCountingMetrics()
	server := startUDPServer(t, metrics, WithAddr("127.0.0.1:0"), WithMaxSessions(10), WithMaxSessionsPerPeer(1))
	addr := server.Addr().String()

	sendFrom(t, "127.0.0.2", addr)
	waitFor(t, "the first peer's session", func() bool { return server.SessionCount() == 1 })

	// Same address, different port: the peer is the address, not the socket.
	sendFrom(t, "127.0.0.2", addr)
	// A different address must still be admitted - that is the whole point of
	// splitting the two dimensions.
	sendFrom(t, "127.0.0.3", addr)
	waitFor(t, "the second peer's session", func() bool { return server.SessionCount() == 2 })

	if got := metrics.get("sessions_rejected_total"); got < 1 {
		t.Fatalf("sessions_rejected_total = %d, want at least 1 (the second socket of 127.0.0.2)", got)
	}
	if got := server.SessionCount(); got != 2 {
		t.Fatalf("SessionCount = %d, want 2 (one per address)", got)
	}
}

// TestUDPMaxSessionsHoldsUnderManySourceAddresses is A1's flood acceptance
// criterion in miniature.
//
// The real attack forges 100k source addresses, which the test cannot do
// without raw sockets. It does not need to: the property under test is that the
// session table is bounded by MaxSessions no matter how many distinct addresses
// arrive, and each admission costs one bounded map entry, so 200 addresses
// exercise the same code path as 100k with the same constant memory ceiling.
func TestUDPMaxSessionsHoldsUnderManySourceAddresses(t *testing.T) {
	const (
		maxSessions = 64
		flood       = 200
	)
	metrics := newCountingMetrics()
	server := startUDPServer(t, metrics, WithAddr("127.0.0.1:0"),
		WithMaxSessions(maxSessions),
		// Long enough that the table cannot drain mid-flood, so a passing
		// count is the cap holding rather than the sweep catching up.
		WithSessionTTL(10*time.Second),
		WithSweepInterval(50*time.Millisecond))
	addr := server.Addr().String()

	for i := 0; i < flood; i++ {
		// 127.1.x.7: distinct addresses, all routed to the loopback interface.
		sendFrom(t, fmt.Sprintf("127.1.%d.7", i), addr)
	}
	waitFor(t, "the table to fill to its ceiling", func() bool { return server.SessionCount() == maxSessions })
	time.Sleep(100 * time.Millisecond)

	if got := server.SessionCount(); got != maxSessions {
		t.Fatalf("SessionCount = %d after %d source addresses, want the %d ceiling", got, flood, maxSessions)
	}
	if got := metrics.get("sessions_rejected_total"); got != flood-maxSessions {
		t.Fatalf("sessions_rejected_total = %d, want %d (one per address over the ceiling)", got, flood-maxSessions)
	}
}

// TestUDPMaxSessionsReleasesOnExpiry is the other half of A1: a rejection is
// only acceptable if the table drains, otherwise a short flood would take the
// transport down for good.
func TestUDPMaxSessionsReleasesOnExpiry(t *testing.T) {
	metrics := newCountingMetrics()
	server := startUDPServer(t, metrics, WithAddr("127.0.0.1:0"),
		WithMaxSessions(1),
		WithSessionTTL(80*time.Millisecond),
		WithSweepInterval(10*time.Millisecond))
	addr := server.Addr().String()

	sendFrom(t, "127.0.0.2", addr)
	waitFor(t, "the first session", func() bool { return server.SessionCount() == 1 })

	// The table is full: a second peer is dropped.
	sendFrom(t, "127.0.0.3", addr)
	time.Sleep(50 * time.Millisecond)
	if got := metrics.get("sessions_rejected_total"); got < 1 {
		t.Fatalf("sessions_rejected_total = %d, want at least 1 while the table is full", got)
	}

	// After the idle sweep the slot comes back, and the same peer that was
	// refused now gets in.
	waitFor(t, "the table to drain", func() bool { return server.SessionCount() == 0 })
	before := metrics.get("sessions_rejected_total")
	sendFrom(t, "127.0.0.3", addr)
	waitFor(t, "the refused peer to be admitted after the drain", func() bool { return server.SessionCount() == 1 })
	if got := metrics.get("sessions_rejected_total"); got != before {
		t.Fatalf("sessions_rejected_total moved from %d to %d while the table had room", before, got)
	}
}
