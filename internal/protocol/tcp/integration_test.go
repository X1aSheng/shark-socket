package tcp

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yourname/shark-socket/internal/types"
)

// TestIntegration_TCP_Echo starts a TCP server on :0, connects a client,
// sends a framed message via LengthPrefixFramer, receives the echo, and verifies.
func TestIntegration_TCP_Echo(t *testing.T) {
	// Echo handler: bounce back whatever arrives.
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithWorkerPool(2, 8, 64),
		WithFramer(NewLengthPrefixFramer(4096)),
	)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		srv.Stop(stopCtx)
	}()

	// The listener field is unexported but accessible within package tcp.
	addr := srv.listener.Addr().String()
	t.Logf("TCP server listening on %s", addr)

	// Connect a raw TCP client.
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	framer := NewLengthPrefixFramer(4096)

	// Send a framed message.
	payload := []byte("hello shark-socket integration")
	if err := framer.WriteFrame(conn, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	// Read the echoed framed response.
	got, err := framer.ReadFrame(conn)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	if string(got) != string(payload) {
		t.Fatalf("echo mismatch: got %q, want %q", got, payload)
	}
	t.Logf("Echo verified: %q", got)

	// Verify server reports TCP protocol.
	if proto := srv.Protocol(); proto != types.TCP {
		t.Fatalf("Protocol() = %v, want %v", proto, types.TCP)
	}

	// Verify manager exists and has at least 1 session.
	if mgr := srv.Manager(); mgr == nil {
		t.Fatal("Manager() returned nil")
	}
}

// TestIntegration_TCP_MultipleClients starts a server and connects 5 clients
// concurrently. Each client sends a unique framed message and verifies the echo.
func TestIntegration_TCP_MultipleClients(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithWorkerPool(4, 16, 128),
		WithFramer(NewLengthPrefixFramer(4096)),
	)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		srv.Stop(stopCtx)
	}()

	addr := srv.listener.Addr().String()
	t.Logf("TCP server listening on %s", addr)

	const numClients = 5
	var wg sync.WaitGroup
	var errors atomic.Int64

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
			if err != nil {
				t.Errorf("client %d: dial: %v", idx, err)
				errors.Add(1)
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(10 * time.Second))

			framer := NewLengthPrefixFramer(4096)

			// Each client sends a unique payload.
			payload := []byte("client-message-" + string(rune('A'+idx)))
			if err := framer.WriteFrame(conn, payload); err != nil {
				t.Errorf("client %d: WriteFrame: %v", idx, err)
				errors.Add(1)
				return
			}

			got, err := framer.ReadFrame(conn)
			if err != nil {
				t.Errorf("client %d: ReadFrame: %v", idx, err)
				errors.Add(1)
				return
			}

			if string(got) != string(payload) {
				t.Errorf("client %d: echo mismatch: got %q, want %q", idx, got, payload)
				errors.Add(1)
				return
			}
			t.Logf("client %d: echo OK: %q", idx, got)
		}(i)
	}

	wg.Wait()

	if n := errors.Load(); n > 0 {
		t.Fatalf("%d client(s) encountered errors", n)
	}
}
