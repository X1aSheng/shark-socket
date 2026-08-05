package udp

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// TestUDPSessionWedgeRegression verifies that a handler error closes and
// removes the UDP session so the peer is not permanently stuck on a closed
// session (the "wedge" bug). After one error, the next datagram must reach
// the handler again.
func TestUDPSessionWedgeRegression(t *testing.T) {
	calls := 0
	handler := func(core.Session, core.Message) error {
		calls++
		if calls == 1 {
			return errors.New("boom")
		}
		return nil
	}

	srv := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(handler),
	)
	rt := runtime.NewRuntime(nil, nil)
	srv.UseRuntime(rt)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop(context.Background())

	conn, err := net.Dial("udp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// First datagram triggers the error path.
	if _, err := conn.Write([]byte("first")); err != nil {
		t.Fatalf("write1: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Second datagram must reach the handler (new session, not wedged).
	if _, err := conn.Write([]byte("second")); err != nil {
		t.Fatalf("write2: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if calls >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if calls < 2 {
		t.Fatalf("handler calls = %d, want >= 2 (peer was wedged to a closed session)", calls)
	}
}
