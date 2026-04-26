package http

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

func waitForTCPServer(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("TCP server at %s not ready after %v", addr, timeout)
}

// ---------------------------------------------------------------------------
// Mode A tests — thin net/http wrapper
// ---------------------------------------------------------------------------

func TestNewServer(t *testing.T) {
	s := NewServer()
	if s == nil {
		t.Fatal("NewServer() returned nil")
	}
	if s.Protocol() != types.HTTP {
		t.Errorf("Protocol() = %v, want HTTP", s.Protocol())
	}
}

func TestNewServerWithOptions(t *testing.T) {
	s := NewServer(
		WithAddr("127.0.0.1", 0),
		WithTimeouts(1, 1, 1),
	)
	if s == nil {
		t.Fatal("NewServer() returned nil")
	}
}

func TestHandleFunc(t *testing.T) {
	s := NewServer()
	called := false
	s.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	})

	// Use httptest to exercise the mux
	ts := httptest.NewServer(s.mux)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/ping")
	if err != nil {
		t.Fatalf("GET /ping: %v", err)
	}
	defer resp.Body.Close()

	if !called {
		t.Error("handler was not called")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "pong" {
		t.Errorf("body = %q, want %q", body, "pong")
	}
}

func TestHandleFunc_NotFound(t *testing.T) {
	s := NewServer()
	s.HandleFunc("/exists", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(s.mux)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/nonexistent")
	if err != nil {
		t.Fatalf("GET /nonexistent: %v", err)
	}
	defer resp.Body.Close()

	// Default mux returns 404 for unregistered paths
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Mode B tests — per-request session with SetHandler
// ---------------------------------------------------------------------------

func TestServer_SetHandler_ModeB(t *testing.T) {
	s := NewServer()

	var receivedSessionID uint64
	var receivedPayload []byte

	s.SetHandler(func(sess types.RawSession, msg types.RawMessage) error {
		receivedSessionID = sess.ID()
		receivedPayload = msg.Payload
		sess.Send([]byte("response from handler"))
		return nil
	})

	// Manually invoke handleWithSession via httptest
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("test payload"))
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	s.handleWithSession(w, req)

	if receivedSessionID == 0 {
		t.Error("expected non-zero session ID")
	}
	if string(receivedPayload) != "test payload" {
		t.Errorf("payload = %q, want %q", receivedPayload, "test payload")
	}

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "response from handler" {
		t.Errorf("response body = %q, want %q", body, "response from handler")
	}
}

func TestServer_SetHandler_ModeB_EmptyBody(t *testing.T) {
	s := NewServer()

	handlerCalled := false
	s.SetHandler(func(sess types.RawSession, msg types.RawMessage) error {
		handlerCalled = true
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	s.handleWithSession(w, req)

	// Handler should be called even with empty body
	if !handlerCalled {
		t.Error("handler should be called even with empty body")
	}
}

// ---------------------------------------------------------------------------
// Start / Stop lifecycle
// ---------------------------------------------------------------------------

func TestServer_StartStop(t *testing.T) {
	// Find a free port
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	lis.Close()

	s := NewServer(WithAddr("127.0.0.1", port))
	s.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	// Give server a moment to start
	waitForTCPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	// Verify the server is reachable
	resp, err := http.Get("http://127.0.0.1:" + itoa(port) + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
}

func TestServer_StopWithoutStart(t *testing.T) {
	s := NewServer()
	ctx := context.Background()
	if err := s.Stop(ctx); err != nil {
		t.Errorf("Stop() on unstarted server: %v", err)
	}
}

func TestServer_StopIdempotent(t *testing.T) {
	s := NewServer()
	s.Start()
	waitForTCPServer(t, s.opts.Addr(), 3*time.Second)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := s.Stop(ctx); err != nil {
			t.Errorf("Stop() call %d error: %v", i, err)
		}
	}
}

func TestServer_Protocol(t *testing.T) {
	s := NewServer()
	if p := s.Protocol(); p != types.HTTP {
		t.Errorf("Protocol() = %v, want HTTP", p)
	}
}

// itoa converts int to string (simple helper to avoid strconv import).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	pos := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
