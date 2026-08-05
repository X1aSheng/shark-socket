package tcp

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestSessionMethods(t *testing.T) {
	conn1, conn2 := net.Pipe()
	defer conn1.Close()
	defer conn2.Close()
	_ = conn2 // keep reference to prevent GC

	sess := newSession(42, conn1, LengthPrefixFramer{MaxFrameBytes: 1024}, 128, 30*time.Second, 0.8)

	// Protocol
	if got := sess.Protocol(); got != core.ProtocolTCP {
		t.Errorf("Protocol() = %v, want %v", got, core.ProtocolTCP)
	}

	// RemoteAddr / LocalAddr
	if got := sess.RemoteAddr(); got == nil {
		t.Error("RemoteAddr() = nil")
	}
	if got := sess.LocalAddr(); got == nil {
		t.Error("LocalAddr() = nil")
	}

	// ID
	if got := sess.ID(); got != 42 {
		t.Errorf("ID() = %d, want 42", got)
	}

	// State
	if got := sess.State(); got != core.StateActive {
		t.Errorf("State() = %v, want %v", got, core.StateActive)
	}

	// CreatedAt
	if got := sess.CreatedAt(); got.IsZero() {
		t.Error("CreatedAt() is zero")
	}

	// LastActiveAt
	if got := sess.LastActiveAt(); got.IsZero() {
		t.Error("LastActiveAt() is zero")
	}

	// Context
	if got := sess.Context(); got == nil {
		t.Error("Context() = nil")
	}

	// SetMeta / GetMeta / DelMeta
	sess.SetMeta("key1", "value1")
	got, ok := sess.GetMeta("key1")
	if !ok || got != "value1" {
		t.Errorf("GetMeta after SetMeta = (%v, %v), want (value1, true)", got, ok)
	}
	sess.DelMeta("key1")
	if _, ok := sess.GetMeta("key1"); ok {
		t.Error("GetMeta after DelMeta returned ok=true")
	}
}

// TestSessionSendCloseRace is a regression test for the Send/Close
// concurrent-close panic (send on closed channel). It hammers Send from
// multiple goroutines while Close runs concurrently; the test only fails
// if a panic or unexpected error escapes.
func TestSessionSendCloseRace(t *testing.T) {
	conn1, conn2 := net.Pipe()
	defer conn1.Close()
	defer conn2.Close()

	sess := newSession(1, conn1, LengthPrefixFramer{MaxFrameBytes: 1024}, 128, time.Second, 0.8)

	// Drain writes so net.Pipe does not block the writer forever.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := conn2.Read(buf); err != nil {
				return
			}
		}
	}()

	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				_ = sess.Send([]byte("hello"))
			}
		}()
	}
	for i := 0; i < 200; i++ {
		_ = sess.Close(context.Background())
	}
	close(done)
}

// TestPoolSubmitStopRace is a regression test for the submit/stop race that
// could panic with "send on closed channel" when stop() closed the task queue.
func TestPoolSubmitStopRace(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		p := newWorkerPool(nil, 1, 2, PolicyBlock)
		p.start(1)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = p.submit(nil, []byte("x"))
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(5 * time.Millisecond)
			p.stop()
			close(stop)
		}()
		wg.Wait()
	}
}
