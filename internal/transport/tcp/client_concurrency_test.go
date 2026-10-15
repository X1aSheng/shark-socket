package tcp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// startEchoServer starts a minimal TCP echo server for the client tests.
func startEchoServer(t *testing.T) *Server {
	t.Helper()
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start tcp server: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Stop(stopCtx)
	})
	return server
}

// TestClientConnectTwiceReleasesPreviousConn pins the fix for a socket leak:
// calling Connect twice used to overwrite the previous net.Conn without
// closing it, so the old socket stayed open (and unreachable) until the GC
// finalizer happened to run.
func TestClientConnectTwiceReleasesPreviousConn(t *testing.T) {
	server := startEchoServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := NewClient(server.Addr().String(), WithClientLinger(0))
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	first := client.currentConnOrNil()
	if first == nil {
		t.Fatal("Connect did not install a connection")
	}

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("second connect: %v", err)
	}

	// The previous connection must have been released. A successful write
	// proves it is still open, i.e. leaked.
	if _, err := first.Write([]byte("probe")); err == nil {
		t.Fatal("re-Connect leaked the previous connection: it is still writable")
	}
}

// TestClientConcurrentUseIsRaceFree exercises Connect/Send/Receive/Close from
// separate goroutines. Before the fix Client had no synchronization at all and
// this trips the race detector on the conn field.
func TestClientConcurrentUseIsRaceFree(t *testing.T) {
	server := startEchoServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := NewClient(
		server.Addr().String(),
		WithClientLinger(0),
		WithClientReadTimeout(50*time.Millisecond),
	)

	const iters = 50
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = client.Connect(ctx)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = client.Send([]byte("ping"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_, _ = client.Receive()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = client.Close()
		}
	}()
	wg.Wait()
}
