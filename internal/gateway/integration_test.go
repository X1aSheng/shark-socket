package gateway

import (
	"context"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	httpproto "github.com/X1aSheng/shark-socket/internal/protocol/http"
	tcpproto "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// TestIntegration_Gateway_MultiProtocol creates a gateway, registers a TCP server
// and an HTTP server, starts both, verifies both protocols respond correctly,
// then performs a graceful stop.
func TestIntegration_Gateway_MultiProtocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Find free ports for both protocols.
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find tcp port: %v", err)
	}
	tcpPort := tcpLn.Addr().(*net.TCPAddr).Port
	tcpLn.Close()

	hLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find http port: %v", err)
	}
	httpPort := hLn.Addr().(*net.TCPAddr).Port
	hLn.Close()

	// TCP echo server
	echoHandler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}
	tcpSrv := tcpproto.NewServer(echoHandler,
		tcpproto.WithAddr("127.0.0.1", tcpPort),
		tcpproto.WithWorkerPool(2, 8, 64),
		tcpproto.WithFramer(tcpproto.NewLengthPrefixFramer(4096)),
	)

	// HTTP server with /health route
	httpSrv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", httpPort))
	httpSrv.HandleFunc("/health", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
		w.Write([]byte("gateway-ok"))
	})

	// Create gateway and register both servers.
	gw := New(WithMetricsEnabled(false))
	if err := gw.Register(tcpSrv); err != nil {
		t.Fatalf("register TCP: %v", err)
	}
	if err := gw.Register(httpSrv); err != nil {
		t.Fatalf("register HTTP: %v", err)
	}

	// Start the gateway (starts all registered servers).
	if err := gw.Start(); err != nil {
		t.Fatalf("gateway Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := gw.Stop(stopCtx); err != nil {
			t.Logf("gateway Stop: %v", err)
		}
	}()

	// Allow servers time to start.
	time.Sleep(200 * time.Millisecond)

	// --- Verify TCP ---
	t.Run("TCPEcho", func(t *testing.T) {
		addr := fmt.Sprintf("127.0.0.1:%d", tcpPort)
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			t.Fatalf("tcp dial %s: %v", addr, err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))

		framer := tcpproto.NewLengthPrefixFramer(4096)
		payload := []byte("gateway-tcp-test")
		if err := framer.WriteFrame(conn, payload); err != nil {
			t.Fatalf("tcp WriteFrame: %v", err)
		}

		got, err := framer.ReadFrame(conn)
		if err != nil {
			t.Fatalf("tcp ReadFrame: %v", err)
		}

		if string(got) != string(payload) {
			t.Fatalf("tcp echo mismatch: got %q, want %q", got, payload)
		}
		t.Logf("TCP echo via gateway verified: %q", got)
	})

	// --- Verify HTTP ---
	t.Run("HTTPHealth", func(t *testing.T) {
		url := fmt.Sprintf("http://127.0.0.1:%d/health", httpPort)
		client := &stdhttp.Client{Timeout: 5 * time.Second}

		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != stdhttp.StatusOK {
			t.Fatalf("HTTP status = %d, want %d", resp.StatusCode, stdhttp.StatusOK)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		if string(body) != "gateway-ok" {
			t.Fatalf("HTTP body = %q, want %q", body, "gateway-ok")
		}
		t.Logf("HTTP health via gateway verified: %q", body)
	})

	// Verify gateway protocol returns Custom.
	if proto := gw.Protocol(); proto != types.Custom {
		t.Fatalf("Protocol() = %v, want %v", proto, types.Custom)
	}

	// Verify shared manager exists.
	if mgr := gw.Manager(); mgr == nil {
		t.Fatal("Manager() returned nil after Start()")
	}

	_ = ctx
}

// TestIntegration_Gateway_StopIdempotent verifies that calling Stop multiple
// times does not panic or return unexpected errors.
func TestIntegration_Gateway_StopIdempotent(t *testing.T) {
	gw := New(WithMetricsEnabled(false))

	// Register a minimal mock server so Start succeeds.
	gw.Register(&testMockServer{proto: types.TCP})

	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()
	if err := gw.Stop(ctx1); err != nil {
		t.Fatalf("first Stop: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := gw.Stop(ctx2); err != nil {
		t.Fatalf("second Stop (should be no-op): %v", err)
	}
}

// testMockServer is a minimal types.Server implementation for gateway tests.
type testMockServer struct {
	proto   types.ProtocolType
	started atomic.Bool
}

func (m *testMockServer) Start() error                 { m.started.Store(true); return nil }
func (m *testMockServer) Stop(_ context.Context) error { m.started.Store(false); return nil }
func (m *testMockServer) Protocol() types.ProtocolType { return m.proto }
