package tcp

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// countingMetrics records counter names so the tests can assert that a refusal
// or a failure was reported, not only that it happened.
type countingMetrics struct {
	mu     sync.Mutex
	counts map[string]int
}

func newCountingMetrics() *countingMetrics {
	return &countingMetrics{counts: map[string]int{}}
}

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

// observableRuntime substitutes a metrics sink for the one the runtime builds
// for itself. The runtime's own metrics option is unexported, so a test outside
// the runtime package shadows the accessor instead.
type observableRuntime struct {
	*runtime.Runtime
	metrics core.Metrics
}

func (r observableRuntime) Metrics() core.Metrics { return r.metrics }

func newObservableRuntime(m core.Metrics) core.Runtime {
	return observableRuntime{Runtime: runtime.NewRuntime(nil, nil), metrics: m}
}

// TestTCPPerPeerConnectionLimit is A5: the global ceiling must not be reachable
// by a single peer.
func TestTCPPerPeerConnectionLimit(t *testing.T) {
	metrics := newCountingMetrics()
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		// Global room for ten, one per peer: only the per-peer rule can refuse
		// the second connection, so the test cannot pass for the wrong reason.
		WithMaxConnections(10),
		WithMaxConnectionsPerIP(1),
		WithHandler(echoHandler),
	)
	server.rt = newObservableRuntime(metrics)
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Stop(ctx)
	}()

	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	first, err := dialWithLinger(context.Background(), "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := framer.WriteFrame(first, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if got, err := framer.ReadFrame(first); err != nil || string(got) != "ping" {
		t.Fatalf("first connection (frame %q, err %v), want a served echo", got, err)
	}

	// Same address, same peer: refused although the global cap still has nine
	// slots free.
	second, err := dialWithLinger(context.Background(), "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := framer.WriteFrame(second, []byte("ping")); err == nil {
		if _, err := framer.ReadFrame(second); err == nil {
			t.Fatal("second connection from one peer was served despite MaxConnectionsPerIP=1")
		}
	}
	if got := metrics.get("sessions_rejected_total"); got != 1 {
		t.Fatalf("sessions_rejected_total = %d, want 1", got)
	}

	// Releasing the slot must let the same peer back in, or the quota would leak
	// a slot per refused attempt.
	_ = first.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		third, err := dialWithLinger(context.Background(), "tcp", server.Addr().String())
		if err == nil {
			_ = framer.WriteFrame(third, []byte("ping"))
			got, readErr := framer.ReadFrame(third)
			_ = third.Close()
			if readErr == nil && string(got) == "ping" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer was not re-admitted after releasing its slot (last dial error %v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTCPTLSHandshakeTimeoutIsEnforced is A2: a peer that sends a connection and
// then nothing must not hold a slot for the whole ReadTimeout.
func TestTCPTLSHandshakeTimeoutIsEnforced(t *testing.T) {
	metrics := newCountingMetrics()
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(testServerTLSConfig(t)),
		WithTLSHandshakeTimeout(150*time.Millisecond),
		// Deliberately long: the point is that the handshake budget, not the
		// session budget, reclaims the connection.
		WithReadTimeout(5*time.Minute),
		WithHandler(echoHandler),
	)
	server.rt = newObservableRuntime(metrics)
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Stop(ctx)
	}()

	conn, err := net.Dial("tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Say nothing at all: no ClientHello, no bytes.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	_, err = conn.Read(make([]byte, 1))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("silent TLS peer was not disconnected")
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("connection still open after %v; the handshake timeout was not applied", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("connection closed after %v, want about the 150ms handshake timeout", elapsed)
	}
	if got := metrics.get("tls_handshake_failed_total"); got != 1 {
		t.Fatalf("tls_handshake_failed_total = %d, want 1", got)
	}
}

// TestTCPTLSHandshakeSucceedsThenServes guards the other side of A2: the
// deadline set for the handshake must be cleared, or a healthy connection dies
// when it expires.
func TestTCPTLSHandshakeSucceedsThenServes(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(testServerTLSConfig(t)),
		WithTLSHandshakeTimeout(200*time.Millisecond),
		WithHandler(echoHandler),
	)
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Stop(ctx)
	}()

	conn, err := tlsDial(server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Wait past the handshake deadline: the session must still be usable, which
	// only holds if handshakeTLS cleared the deadline it set.
	time.Sleep(350 * time.Millisecond)
	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	if err := framer.WriteFrame(conn, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if got, err := framer.ReadFrame(conn); err != nil || string(got) != "ping" {
		t.Fatalf("frame %q, err %v after the handshake deadline passed", got, err)
	}
}

// flakyListener fails the first len(errs) Accept calls, then serves normally.
type flakyListener struct {
	net.Listener
	mu       sync.Mutex
	errs     []error
	accepted atomic.Int64
}

func (l *flakyListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if len(l.errs) > 0 {
		err := l.errs[0]
		l.errs = l.errs[1:]
		l.mu.Unlock()
		return nil, err
	}
	l.mu.Unlock()
	conn, err := l.Listener.Accept()
	if err == nil {
		l.accepted.Add(1)
	}
	return conn, err
}

// TestTCPAcceptLoopSurvivesResourceErrors is A4: EMFILE must be counted, backed
// off, and retried rather than spun on, and the loop must resume by itself once
// the resource comes back.
func TestTCPAcceptLoopSurvivesResourceErrors(t *testing.T) {
	metrics := newCountingMetrics()
	real, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer real.Close()

	emfile := &net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept", syscall.EMFILE)}
	flaky := &flakyListener{Listener: real, errs: []error{emfile, emfile}}

	server := NewServer(WithAddr("127.0.0.1:0"), WithHandler(echoHandler))
	server.rt = newObservableRuntime(metrics)
	server.listener = flaky

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.acceptWG.Add(1)
	go server.acceptLoop(ctx)

	// The listener refuses twice before it serves; the client retries until it
	// gets through, which only happens if the loop backed off instead of dying.
	start := time.Now()
	var conn net.Conn
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err = net.Dial("tcp", real.Addr().String())
		if err == nil && flaky.accepted.Load() > 0 {
			break
		}
		if conn != nil {
			_ = conn.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("accept loop did not recover from EMFILE (accepted %d)", flaky.accepted.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer conn.Close()

	// Two failures, each with a backoff: the loop must have waited, not spun.
	if elapsed := time.Since(start); elapsed < 2*5*time.Millisecond {
		t.Fatalf("recovered in %v; the loop did not back off between retries", elapsed)
	}
	if got := metrics.get("accept_errors_total"); got != 2 {
		t.Errorf("accept_errors_total = %d, want 2", got)
	}
	if got := metrics.get("accept_resource_errors_total"); got != 2 {
		t.Errorf("accept_resource_errors_total = %d, want 2", got)
	}
}

// TestTCPAcceptLoopStopsOnClosedListener pins the other half of A4: a closed
// listener is not a temporary failure, so the loop must return instead of
// retrying a socket that will never come back.
func TestTCPAcceptLoopStopsOnClosedListener(t *testing.T) {
	real, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(WithAddr("127.0.0.1:0"), WithHandler(echoHandler))
	server.rt = newObservableRuntime(newCountingMetrics())
	server.listener = real

	done := make(chan struct{})
	server.acceptWG.Add(1)
	go func() {
		server.acceptLoop(context.Background())
		close(done)
	}()
	_ = real.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("accept loop kept running after the listener was closed")
	}
}

// tlsDial connects with a TLS handshake and accepts the test certificate.
func tlsDial(addr string) (*tls.Conn, error) {
	return tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test certificate
}
