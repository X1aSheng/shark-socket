package tcp

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// --- Session tests ---

func TestNewTCPSession_StartsActive(t *testing.T) {
	c, _ := net.Pipe()
	defer c.Close()

	sess := NewTCPSession(1, c, NewLengthPrefixFramer(1024), 16)

	if sess.State() != types.Active {
		t.Errorf("expected Active state, got %v", sess.State())
	}
	if !sess.IsAlive() {
		t.Error("new session should be alive")
	}
	if sess.ID() != 1 {
		t.Errorf("expected ID=1, got %d", sess.ID())
	}
	if sess.Protocol() != types.TCP {
		t.Errorf("expected TCP protocol, got %v", sess.Protocol())
	}
}

func TestTCPSession_SendEnqueues(t *testing.T) {
	c, s := net.Pipe()
	defer c.Close()

	framer := NewLengthPrefixFramer(1024)
	sess := NewTCPSession(1, s, framer, 16)

	// Start WriteLoop to drain the queue
	go sess.WriteLoop()

	if err := sess.Send([]byte("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Read from the pipe to verify data was written
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("read from pipe: %v", err)
	}

	// The framer writes a 4-byte length prefix + payload
	if n != 4+5 {
		t.Errorf("expected %d bytes, got %d", 4+5, n)
	}

	sess.Close()
}

func TestTCPSession_SendOnClosedSession(t *testing.T) {
	c, s := net.Pipe()
	defer c.Close()

	sess := NewTCPSession(1, s, NewLengthPrefixFramer(1024), 16)
	go sess.WriteLoop()
	sess.Close()

	err := sess.Send([]byte("hello"))
	if !errors.Is(err, errs.ErrSessionClosed) {
		t.Errorf("expected ErrSessionClosed, got %v", err)
	}
}

func TestTCPSession_CloseDrainsWriteQueue(t *testing.T) {
	c, s := net.Pipe()

	framer := NewLengthPrefixFramer(1024)
	sess := NewTCPSession(1, s, framer, 64)

	// Start WriteLoop
	go sess.WriteLoop()

	// Send data before closing
	if err := sess.Send([]byte("before-close")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Close should drain the queue
	c.SetReadDeadline(time.Now().Add(3 * time.Second))

	// Read the framed message
	got, err := framer.ReadFrame(c)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != "before-close" {
		t.Errorf("payload mismatch: got %q, want %q", got, "before-close")
	}

	sess.Close()
	c.Close()
}

func TestTCPSession_CloseIsIdempotent(t *testing.T) {
	c, _ := net.Pipe()

	sess := NewTCPSession(1, c, NewLengthPrefixFramer(1024), 16)
	go sess.WriteLoop()

	// Close multiple times should not panic
	if err := sess.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("third Close: %v", err)
	}

	if sess.State() != types.Closed {
		t.Errorf("expected Closed state, got %v", sess.State())
	}
}

func TestTCPSession_SendWriteQueueFull(t *testing.T) {
	c, _ := net.Pipe()

	// Very small write queue, no WriteLoop running to drain it
	sess := NewTCPSession(1, c, NewLengthPrefixFramer(1024), 2)

	// Fill the queue
	if err := sess.Send([]byte("msg1")); err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	if err := sess.Send([]byte("msg2")); err != nil {
		t.Fatalf("Send 2: %v", err)
	}

	// Third send should fail with queue full
	err := sess.Send([]byte("msg3"))
	if !errors.Is(err, errs.ErrWriteQueueFull) {
		t.Errorf("expected ErrWriteQueueFull, got %v", err)
	}

	c.Close()
	// Start WriteLoop so Close() doesn't hang on drain
	go sess.WriteLoop()
	sess.Close()
}

func TestTCPSession_ContextCancelledAfterClose(t *testing.T) {
	c, _ := net.Pipe()

	sess := NewTCPSession(1, c, NewLengthPrefixFramer(1024), 16)
	go sess.WriteLoop()

	ctx := sess.Context()
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}

	// Context should not be done yet
	select {
	case <-ctx.Done():
		t.Fatal("context should not be done before close")
	default:
	}

	sess.Close()

	// After close, context should be cancelled
	select {
	case <-ctx.Done():
		// Expected
	case <-time.After(2 * time.Second):
		t.Fatal("context should be cancelled after close")
	}
}

func TestTCPSession_MetaData(t *testing.T) {
	c, _ := net.Pipe()
	defer c.Close()

	sess := NewTCPSession(1, c, NewLengthPrefixFramer(1024), 16)

	// Set and get metadata
	sess.SetMeta("user", "alice")
	val, ok := sess.GetMeta("user")
	if !ok {
		t.Fatal("expected metadata to exist")
	}
	if val != "alice" {
		t.Errorf("got %v, want %v", val, "alice")
	}

	// Delete metadata
	sess.DelMeta("user")
	_, ok = sess.GetMeta("user")
	if ok {
		t.Error("expected metadata to be deleted")
	}
}

func TestTCPSession_RemoteAddr(t *testing.T) {
	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()

	sess := NewTCPSession(1, s, NewLengthPrefixFramer(1024), 16)

	if sess.RemoteAddr() == nil {
		t.Error("expected non-nil RemoteAddr")
	}
	if sess.LocalAddr() == nil {
		t.Error("expected non-nil LocalAddr")
	}
}

func TestTCPSession_SendTyped(t *testing.T) {
	c, s := net.Pipe()
	defer c.Close()

	framer := NewLengthPrefixFramer(1024)
	sess := NewTCPSession(1, s, framer, 16)

	go sess.WriteLoop()

	// SendTyped without encoder should behave like Send
	if err := sess.SendTyped([]byte("typed")); err != nil {
		t.Fatalf("SendTyped: %v", err)
	}

	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := framer.ReadFrame(c)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != "typed" {
		t.Errorf("payload mismatch: got %q, want %q", got, "typed")
	}

	sess.Close()
}

func TestTCPSession_SendTypedWithEncoder(t *testing.T) {
	c, s := net.Pipe()
	defer c.Close()

	framer := NewLengthPrefixFramer(1024)
	sess := NewTCPSession(1, s, framer, 16)

	// Set an encoder that prepends a prefix
	sess.encoder = func(data []byte) ([]byte, error) {
		out := make([]byte, 0, len(data)+3)
		out = append(out, "pre"...)
		out = append(out, data...)
		return out, nil
	}

	go sess.WriteLoop()

	if err := sess.SendTyped([]byte("data")); err != nil {
		t.Fatalf("SendTyped: %v", err)
	}

	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := framer.ReadFrame(c)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != "predata" {
		t.Errorf("payload mismatch: got %q, want %q", got, "predata")
	}

	sess.Close()
}

func TestTCPSession_SendTypedEncoderFailure(t *testing.T) {
	c, s := net.Pipe()
	defer c.Close()

	sess := NewTCPSession(1, s, NewLengthPrefixFramer(1024), 16)

	sess.encoder = func(data []byte) ([]byte, error) {
		return nil, errors.New("encode failed")
	}

	err := sess.SendTyped([]byte("data"))
	if !errors.Is(err, errs.ErrEncodeFailure) {
		t.Errorf("expected ErrEncodeFailure, got %v", err)
	}

	go sess.WriteLoop()
	sess.Close()
}

func TestTCPSession_CloseSetsState(t *testing.T) {
	c, _ := net.Pipe()

	sess := NewTCPSession(1, c, NewLengthPrefixFramer(1024), 16)
	go sess.WriteLoop()

	if sess.State() != types.Active {
		t.Errorf("expected Active, got %v", sess.State())
	}

	sess.Close()

	if sess.State() != types.Closed {
		t.Errorf("expected Closed, got %v", sess.State())
	}
	if sess.IsAlive() {
		t.Error("closed session should not be alive")
	}
}

func TestTCPSession_ReadLoop_SubmitsToPool(t *testing.T) {
	c, s := net.Pipe()

	framer := NewLengthPrefixFramer(1024)

	var received atomic.Int32
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		received.Add(1)
		return nil
	}

	pool := NewWorkerPool(handler, nil, 1, 16, 4, PolicyDrop)
	pool.Start()
	defer pool.Stop()

	sess := NewTCPSession(1, s, framer, 16)

	go sess.ReadLoop(pool, nil)
	go sess.WriteLoop()

	// Write a framed message to the client side
	if err := framer.WriteFrame(c, []byte("test-readloop")); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if received.Load() != 1 {
		t.Errorf("expected 1 received, got %d", received.Load())
	}

	c.Close()
	sess.Close()
}
