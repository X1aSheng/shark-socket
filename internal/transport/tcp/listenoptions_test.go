package tcp

import (
	"bytes"
	"context"
	"errors"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	sharkruntime "github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
)

// echoHandler replies with the payload it received.
func echoHandler(sess core.Session, msg core.Message) error {
	return sess.Send(msg.Payload)
}

// TestServerServesWithListenOptions verifies the ListenOptions path end to end:
// a listener created through shared.Listen with a non-default keepalive schedule
// must accept, frame and echo exactly like the plain one. The risk this covers
// is a wrapped listener that silently breaks framing or deadlines.
func TestServerServesWithListenOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(echoHandler),
		WithKeepAlive(30*time.Second, 5*time.Second, 3),
	)
	gateway := sharkruntime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = gateway.Stop(stopCtx)
	}()

	conn, err := dialWithLinger(ctx, "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

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
}

// TestServerReusePort covers both sides of the platform split.
//
// Where SO_REUSEPORT exists, two servers must be able to bind the same address
// and both must serve: that is the whole point of the option, and a build that
// compiled the hook but never applied it would still pass a "starts fine" test.
//
// Where it does not exist (Windows), Start must fail with
// shared.ErrReusePortUnsupported rather than quietly producing a plain
// listener, because an operator who asked for port sharing would otherwise find
// out only when the second process refuses to start.
func TestServerReusePort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := NewServer(WithAddr("127.0.0.1:0"), WithHandler(echoHandler), WithReusePort())
	startErr := first.Start(ctx)

	if goruntime.GOOS != "linux" && goruntime.GOOS != "darwin" {
		if !errors.Is(startErr, shared.ErrReusePortUnsupported) {
			t.Fatalf("Start() on %s err = %v, want shared.ErrReusePortUnsupported", goruntime.GOOS, startErr)
		}
		return
	}
	if startErr != nil {
		t.Fatalf("first Start() err = %v", startErr)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = first.Stop(stopCtx)
	}()

	// Same address, second server: without SO_REUSEPORT this bind fails.
	second := NewServer(WithAddr(first.Addr().String()), WithHandler(echoHandler), WithReusePort())
	if err := second.Start(ctx); err != nil {
		t.Fatalf("second Start(%s) err = %v, want nil with SO_REUSEPORT", first.Addr(), err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = second.Stop(stopCtx)
	}()

	// At least one of the two sockets must actually accept and answer: the
	// kernel decides which, and with SO_REUSEPORT both are in the hash.
	conn, err := dialWithLinger(ctx, "tcp", first.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	if err := framer.WriteFrame(conn, []byte("shared")); err != nil {
		t.Fatal(err)
	}
	got, err := framer.ReadFrame(conn)
	if err != nil {
		t.Fatalf("ReadFrame() err = %v (neither listener served the shared port)", err)
	}
	if want := []byte("shared"); !bytes.Equal(got, want) {
		t.Fatalf("echo = %q, want %q", got, want)
	}
}
