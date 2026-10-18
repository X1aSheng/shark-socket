package tcp

import (
	"bytes"
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
)

// TestServerProxyProtocolReportsRealClient is the acceptance criterion for C4:
// after a PROXY v1 header, the handler sees the client the balancer saw, not
// the balancer. The payload that follows the header in the same stream must
// still arrive intact, which is what proves the parser stops exactly at the
// header.
func TestServerProxyProtocolReportsRealClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var gotRemote atomic.Value
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithProxyProtocol(shared.ProxyProtocolV1),
		WithHandler(func(sess core.Session, msg core.Message) error {
			gotRemote.Store(sess.RemoteAddr().String())
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	conn, err := dialWithLinger(ctx, "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("PROXY TCP4 203.0.113.9 198.51.100.1 5555 443\r\n")); err != nil {
		t.Fatal(err)
	}
	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	if err := framer.WriteFrame(conn, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := framer.ReadFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte("hello"); !bytes.Equal(got, want) {
		t.Fatalf("echo = %q, want %q", got, want)
	}
	remote, _ := gotRemote.Load().(string)
	if remote != "203.0.113.9:5555" {
		t.Fatalf("sess.RemoteAddr() = %q, want 203.0.113.9:5555", remote)
	}
}

// TestServerProxyProtocolRejectsMissingHeader pins the other half of the
// contract: with the header required, a plain connection is closed rather than
// silently accepted, and the server keeps serving the next one.
func TestServerProxyProtocolRejectsMissingHeader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithProxyProtocol(shared.ProxyProtocolV1),
		// Short on purpose: a client that sends fewer bytes than the header
		// needs is indistinguishable from one that sends nothing, so the only
		// way to reject it is the deadline. 5s (the default) would make this
		// test slow, not more correct.
		WithProxyHeaderTimeout(200*time.Millisecond),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	// A well-formed frame with no header in front of it: the listener must not
	// mistake the length prefix for a PROXY line.
	plain, err := dialWithLinger(ctx, "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	if err := framer.WriteFrame(plain, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = plain.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := framer.ReadFrame(plain); err == nil {
		t.Fatal("connection without a PROXY header was accepted")
	}

	good, err := dialWithLinger(ctx, "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()
	if _, err := good.Write([]byte("PROXY TCP4 203.0.113.9 198.51.100.1 5555 443\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := framer.WriteFrame(good, []byte("still alive")); err != nil {
		t.Fatal(err)
	}
	_ = good.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := framer.ReadFrame(good)
	if err != nil {
		t.Fatalf("server stopped serving after a rejected header: %v", err)
	}
	if string(got) != "still alive" {
		t.Fatalf("echo = %q, want %q", got, "still alive")
	}
}

// TestServerWithoutProxyProtocolKeepsSocketPeer guards the zero-value path:
// enabling nothing must leave RemoteAddr as the socket peer.
func TestServerWithoutProxyProtocolKeepsSocketPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var gotRemote atomic.Value
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			gotRemote.Store(sess.RemoteAddr().String())
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	conn, err := dialWithLinger(ctx, "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	if err := framer.WriteFrame(conn, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if _, err := framer.ReadFrame(conn); err != nil {
		t.Fatal(err)
	}
	remote, _ := gotRemote.Load().(string)
	clientAddr := conn.LocalAddr().String()
	if remote != clientAddr {
		t.Fatalf("sess.RemoteAddr() = %q, want the socket peer %q", remote, clientAddr)
	}
	if _, port, err := net.SplitHostPort(remote); err != nil || port == "" {
		t.Fatalf("RemoteAddr() = %q is not host:port", remote)
	}
}
