package coap

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// countingFailListener is a net.Listener whose Accept always fails immediately,
// counting the attempts. It emulates a listener stuck in a persistent error
// state (EMFILE, a listener broken by an outside actor) where a retry loop
// without backoff spins at 100% CPU.
type countingFailListener struct {
	accepts atomic.Int64
}

func (l *countingFailListener) Accept() (net.Conn, error) {
	l.accepts.Add(1)
	return nil, errors.New("simulated persistent accept failure")
}

func (l *countingFailListener) Close() error { return nil }

func (l *countingFailListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

// TestCoAPDTLSAcceptLoopBacksOffOnPersistentError pins the busy-spin fix: when
// Accept keeps failing while the server is still running, the loop must
// throttle its retries instead of consuming a core.
func TestCoAPDTLSAcceptLoopBacksOffOnPersistentError(t *testing.T) {
	ln := &countingFailListener{}
	s := NewServer()
	s.rt = runtime.NewRuntime(nil, nil)
	s.dtlsLn = ln

	ctx, cancel := context.WithCancel(context.Background())
	s.wg.Add(1)
	go s.dtlsAcceptLoop(ctx)

	// Let the loop retry for a fixed window. With the 5ms -> 1s doubling
	// backoff only a handful of attempts fit in this window, whereas a tight
	// retry loop racks up hundreds of thousands.
	const window = 250 * time.Millisecond
	time.Sleep(window)
	got := ln.accepts.Load()
	cancel()
	s.wg.Wait()

	if got == 0 {
		t.Fatal("accept loop never attempted Accept")
	}
	if got > 20 {
		t.Fatalf("accept loop made %d attempts in %v; backoff is not applied", got, window)
	}
}

// TestCoAPReadLoopExitsWhenSocketClosedOutOfBand pins the companion fix: a read
// loop whose socket is closed behind the server's back must terminate instead
// of retrying forever and never draining.
func TestCoAPReadLoopExitsWhenSocketClosedOutOfBand(t *testing.T) {
	s := NewServer(WithAddr("127.0.0.1:0"))
	s.rt = runtime.NewRuntime(nil, nil)
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	s.udpConn = conn

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.wg.Add(1)
	go s.readLoop(ctx)

	// Close the socket without going through StopAccept, so s.closed stays
	// false and only net.ErrClosed can tell the loop the socket is gone.
	if err := conn.Close(); err != nil {
		t.Fatalf("close udp conn: %v", err)
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("readLoop kept retrying after its socket was closed out of band")
	}
}
