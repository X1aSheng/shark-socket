package coap

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// TestCoAPEmptyPayloadReachesHandler verifies that a standard CoAP GET with
// an empty payload (the normal read case) reaches the handler instead of
// being silently dropped by a len(payload) > 0 gate.
func TestCoAPEmptyPayloadReachesHandler(t *testing.T) {
	var handled atomic.Int32
	srv := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(core.Session, core.Message) error {
			handled.Add(1)
			return nil
		}),
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

	req := Message{Type: TypeCON, Code: CodeGet, MessageID: 5, Token: []byte{0x01}}
	data, err := req.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && handled.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if handled.Load() == 0 {
		t.Fatal("handler was not called for empty-payload CON GET")
	}
}

// TestCoAPSessionWedgeRegression verifies that an error returned by the
// plugin chain closes and removes the UDP session so the peer is not
// wedged to a closed session on subsequent requests.
func TestCoAPSessionWedgeRegression(t *testing.T) {
	var calls atomic.Int32
	handler := func(core.Session, core.Message) error {
		if calls.Add(1) == 1 {
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

	send := func(mid uint16, payload []byte) {
		req := Message{Type: TypeCON, Code: CodePost, MessageID: mid, Token: []byte{0x01}}
		if len(payload) > 0 {
			req.Payload = payload
		}
		data, err := req.Marshal()
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := conn.Write(data); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// First request triggers the error path.
	send(1, []byte("first"))
	time.Sleep(100 * time.Millisecond)

	// Second request must reach the handler (new session, not wedged).
	send(2, []byte("second"))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && calls.Load() < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Load() < 2 {
		t.Fatalf("handler calls = %d, want >= 2 (peer was wedged to a closed session)", calls.Load())
	}
}
