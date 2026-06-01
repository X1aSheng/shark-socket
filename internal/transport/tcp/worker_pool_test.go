package tcp

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestWorkerPoolDropPolicy(t *testing.T) {
	pool := newWorkerPool(func(core.Session, core.Message) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	}, 1, 1, PolicyDrop)
	if err := pool.submit(&session{writeCh: make(chan []byte)}, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := pool.submit(&session{writeCh: make(chan []byte)}, []byte("two")); !errors.Is(err, core.ErrWriteQueueFull) {
		t.Fatalf("submit error = %v, want %v", err, core.ErrWriteQueueFull)
	}
	pool.stop()
}

func TestWorkerPoolClosesSessionOnHandlerError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	sess := newSession(1, server, RawFramer{}, 1, 0, 0)
	pool := newWorkerPool(func(core.Session, core.Message) error {
		return errors.New("handler failed")
	}, 1, 1, PolicyBlock)
	pool.start(1)
	if err := pool.submit(sess, []byte("boom")); err != nil {
		t.Fatal(err)
	}
	pool.stop()

	select {
	case <-sess.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("session was not closed after handler error")
	}
	_ = sess.Close(context.Background())
}
