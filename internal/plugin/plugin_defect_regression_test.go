package plugin

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/infra/pubsub"
	"github.com/X1aSheng/shark-socket/internal/infra/store"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// blockingSession is a session whose Close blocks until released, used to
// check that a stuck session cannot wedge the heartbeat sweep (and therefore
// Gateway.Stop).
type blockingSession struct {
	fakeSession
	release chan struct{}
	once    sync.Once
}

func (s *blockingSession) LastActiveAt() time.Time { return time.Now().Add(-time.Hour) }
func (s *blockingSession) Close(context.Context) error {
	<-s.release
	return nil
}

// TestAutoBanBannedAcceptsHostPort verifies that Banned() resolves the same
// key the ban was recorded under. Record/OnAccept key bans by the remote host
// (SplitHostPort), so looking up a "host:port" string never matched and
// Banned() silently reported false for a banned peer.
func TestAutoBanBannedAcceptsHostPort(t *testing.T) {
	p := NewAutoBan(1)
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.7"), Port: 1234}}

	if !p.Record(sess) {
		t.Fatal("first message did not trip the threshold")
	}
	if !p.Banned("10.0.0.7") {
		t.Fatal("Banned(host) = false after the host was banned")
	}
	if !p.Banned("10.0.0.7:1234") {
		t.Fatal("Banned(host:port) = false after the host was banned; the lookup key does not match the recorded key")
	}
	if p.Banned("10.0.0.8") {
		t.Fatal("Banned reported a different host as banned")
	}
}

// TestHeartbeatStopBoundedWhenSessionCloseBlocks verifies that a session whose
// Close never returns cannot block Heartbeat.Stop forever. Sweep called Close
// with context.Background() and no timeout, so a single wedged session kept
// the sweep goroutine alive and Stop (which waits for it) hung — and with it
// Gateway.Stop, which holds the gateway mutex during shutdown.
func TestHeartbeatStopBoundedWhenSessionCloseBlocks(t *testing.T) {
	manager := runtime.NewSessionManager()
	sess := &blockingSession{release: make(chan struct{})}
	if err := manager.Register(sess); err != nil {
		t.Fatal(err)
	}

	h := NewHeartbeat(manager, time.Millisecond, time.Millisecond).WithSweepCloseTimeout(100 * time.Millisecond)
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}

	// Let at least one sweep run and get stuck on the blocking Close.
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		_ = h.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(sess.release) // let the leaked goroutine finish
		t.Fatal("Heartbeat.Stop blocked on a session Close that never returns")
	}
}

// TestClusterTopicIsRaceFree verifies that WithTopic may be called while the
// cluster is consuming messages. topic was read without the lock in Start,
// OnMessage and handleClusterMessage while WithTopic wrote it under the lock,
// so a concurrent call raced on the string header (-race reports it).
func TestClusterTopicIsRaceFree(t *testing.T) {
	manager := runtime.NewSessionManager()
	bus := pubsub.New()
	c := NewCluster("node-1", bus, manager, 4)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Stop() }()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			c.WithTopic("topic-" + strings.Repeat("x", i%8))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = c.OnMessage(fakeSession{}, []byte("payload"))
		}
	}()
	wg.Wait()
}

// TestPersistenceSurfacesInitError verifies that a message-log initialisation
// failure is not silently swallowed. NewPersistence logged through a
// NopLogger, so a broken store left the plugin permanently discarding messages
// with no signal at all.
func TestPersistenceSurfacesInitError(t *testing.T) {
	p := NewPersistence(listFailingStore{}, "sessions")
	if err := p.Err(); err == nil {
		t.Fatal("Persistence.Err() = nil after a failed message-log init; the failure is invisible")
	}
	// Start must propagate the failure so gateway startup aborts loudly
	// instead of running with persistence silently disabled.
	if err := p.Start(); err == nil {
		t.Fatal("Persistence.Start() = nil with a broken store, want an error")
	}
}

// listFailingStore fails List, which is what NewMessageLog calls on open.
type listFailingStore struct{ store.Store }

func (listFailingStore) List(string) ([]string, error)     { return nil, errors.New("list boom") }
func (listFailingStore) Save(string, string, []byte) error { return nil }
func (listFailingStore) Close() error                      { return nil }
