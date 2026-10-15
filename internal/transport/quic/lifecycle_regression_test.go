package quic

import (
	"context"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/runtime"
	quicgo "github.com/quic-go/quic-go"
)

// TestQUICDrainCompletesWithLiveConnection verifies that a staged stop drains
// while a QUIC connection is still open. handleConn blocks in AcceptStream
// until the connection itself is closed, and CloseSessions runs only after
// Drain; without a server-level cancellation signal the two stages wait on
// each other and Drain returns its stage timeout instead of nil.
func TestQUICDrainCompletesWithLiveConnection(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(testTLSConfig(t)),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = gateway.Stop(ctx)
	}()

	conn, err := quicgo.DialAddr(context.Background(), server.Addr().String(), ClientTLSConfig(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseWithError(0, "test done") }()

	// Wait until the server has actually accepted (and registered) the conn.
	deadline := time.Now().Add(3 * time.Second)
	for gateway.Runtime().Sessions().Count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("server never accepted the QUIC connection")
		}
		time.Sleep(5 * time.Millisecond)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.StopAccept(stopCtx); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := server.Drain(stopCtx); err != nil {
		t.Fatalf("Drain with a live connection = %v after %v; want nil", err, time.Since(start))
	}
}

// TestQUICGatewayStopWithLiveConnectionCompletes verifies the user-visible
// path: Gateway.Stop must return nil (not a drained-stage timeout) while a
// peer connection is still open.
func TestQUICGatewayStopWithLiveConnectionCompletes(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithTLS(testTLSConfig(t)),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	conn, err := quicgo.DialAddr(context.Background(), server.Addr().String(), ClientTLSConfig(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseWithError(0, "test done") }()

	deadline := time.Now().Add(3 * time.Second)
	for gateway.Runtime().Sessions().Count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("server never accepted the QUIC connection")
		}
		time.Sleep(5 * time.Millisecond)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := gateway.Stop(stopCtx); err != nil {
		t.Fatalf("gateway.Stop with a live connection = %v after %v; want nil", err, time.Since(start))
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("gateway.Stop took %v, want a prompt shutdown", elapsed)
	}
}

// TestQUICSendAfterCloseReportsClosed verifies that Send never reports a
// false success once the session is closed: the write queue must not win the
// select over the cancelled context.
func TestQUICSendAfterCloseReportsClosed(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"), WithTLS(testTLSConfig(t)))
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = gateway.Stop(ctx)
	}()

	conn, err := quicgo.DialAddr(context.Background(), server.Addr().String(), ClientTLSConfig(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseWithError(0, "test done") }()

	var sess *session
	deadline := time.Now().Add(3 * time.Second)
	for sess == nil {
		server.sessions.Range(func(_, v any) bool {
			sess = v.(*session)
			return false
		})
		if sess == nil {
			if time.Now().After(deadline) {
				t.Fatal("session never registered")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("session close: %v", err)
	}
	for i := 0; i < 200; i++ {
		if err := sess.Send([]byte("late")); err == nil {
			t.Fatalf("Send after Close returned nil on iteration %d; want ErrSessionClosed", i)
		}
	}
}
