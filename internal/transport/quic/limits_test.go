package quic

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	quicgo "github.com/quic-go/quic-go"
)

// dialQUICFrom opens a QUIC connection whose packets leave from localIP, so a
// test can act as several distinct peers without forging anything.
func dialQUICFrom(t *testing.T, serverAddr, localIP string) (*quicgo.Conn, error) {
	t.Helper()
	local := &net.UDPAddr{IP: net.ParseIP(localIP)}
	packetConn, err := net.ListenUDP("udp", local)
	if err != nil {
		t.Fatalf("bind %s: %v", localIP, err)
	}
	tr := &quicgo.Transport{Conn: packetConn}
	t.Cleanup(func() {
		_ = tr.Close()
		_ = packetConn.Close()
	})
	raddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tr.Dial(ctx, raddr, ClientTLSConfig(true), nil)
}

// echoQUIC sends one message on a fresh stream and returns the echoed reply.
func echoQUIC(conn *quicgo.Conn, payload string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return "", err
	}
	if _, err := stream.Write([]byte(payload)); err != nil {
		return "", err
	}
	if err := stream.Close(); err != nil {
		return "", err
	}
	resp, err := conn.AcceptStream(ctx)
	if err != nil {
		return "", err
	}
	defer resp.Close()
	_ = resp.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := io.ReadAll(resp)
	return string(got), err
}

// TestQUICPerPeerConnectionLimit is A5: one host must not be able to occupy the
// whole connection budget, and refusing it must leave other peers served.
func TestQUICPerPeerConnectionLimit(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(testTLSConfig(t)),
		// Global room for ten, one per peer: only the per-peer rule can refuse
		// the second connection.
		WithMaxConnections(10),
		WithMaxConnectionsPerIP(1),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	addr := server.Addr().String()

	first, err := dialQUICFrom(t, addr, "127.0.0.2")
	if err != nil {
		t.Fatalf("first connection from 127.0.0.2: %v", err)
	}
	defer first.CloseWithError(0, "")
	if got, err := echoQUIC(first, "ping"); err != nil || got != "ping" {
		t.Fatalf("echo %q, err %v: the first peer's session is not usable", got, err)
	}

	// Same peer, second connection: the server closes it right after the
	// handshake, which cancels the client-side connection context. (Stream
	// writes are buffered by quic-go and would report success even on a closed
	// connection, so the context is the reliable signal.)
	second, err := dialQUICFrom(t, addr, "127.0.0.2")
	if err == nil {
		defer second.CloseWithError(0, "")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		select {
		case <-second.Context().Done():
		case <-ctx.Done():
			t.Fatal("excess connection from one peer stayed open despite MaxConnectionsPerIP=1")
		}
	}

	// A different peer must still be admitted - that is the point of splitting
	// the two dimensions.
	other, err := dialQUICFrom(t, addr, "127.0.0.1")
	if err != nil {
		t.Fatalf("connection from a second peer: %v", err)
	}
	defer other.CloseWithError(0, "")
	if got, err := echoQUIC(other, "ping"); err != nil || got != "ping" {
		t.Fatalf("echo %q, err %v from the second peer", got, err)
	}
}

// TestQUICRejectedConnectionIsCounted pins the admission counter. A connection
// closed for "server busy" used to leave no trace in the metrics, so an
// overloaded server reported the same numbers as an idle one (D3).
func TestQUICRejectedConnectionIsCounted(t *testing.T) {
	metrics := observability.NewMemoryMetrics()
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(testTLSConfig(t)),
		WithMaxConnections(1),
		WithMaxConnectionsPerIP(1),
		WithHandler(func(core.Session, core.Message) error { return nil }),
	)
	gateway := runtime.NewGateway(runtime.WithMetrics(metrics))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	addr := server.Addr().String()
	first, err := dialQUICFrom(t, addr, "127.0.0.2")
	if err != nil {
		t.Fatalf("first connection with the only slot free: %v", err)
	}
	defer first.CloseWithError(0, "")

	second, err := dialQUICFrom(t, addr, "127.0.0.2")
	if err == nil {
		defer second.CloseWithError(0, "")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		select {
		case <-second.Context().Done():
		case <-ctx.Done():
			t.Fatal("excess connection stayed open despite MaxConnections=1")
		}
	}

	// The refusal happens in the accept loop, one step after the client sees
	// the handshake complete, so the counter is polled rather than read once.
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := metrics.Counter("sessions_rejected_total", "protocol", string(core.ProtocolQUIC))
		if got == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sessions_rejected_total{protocol=quic} = %v, want 1", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
