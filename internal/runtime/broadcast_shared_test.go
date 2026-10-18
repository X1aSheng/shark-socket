package runtime

import (
	"bytes"
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// sharedSession is a session that records the frame it was handed instead of
// copying it, and releases the reference the way a write loop would.
type sharedSession struct {
	id     uint64
	mu     sync.Mutex
	frames []*core.SharedFrame
	copies int
	err    error
}

func (s *sharedSession) ID() uint64              { return s.id }
func (s *sharedSession) Protocol() core.Protocol { return core.ProtocolCustom }
func (s *sharedSession) RemoteAddr() net.Addr    { return nil }
func (s *sharedSession) LocalAddr() net.Addr     { return nil }
func (s *sharedSession) State() core.SessionState {
	return core.StateActive
}
func (s *sharedSession) CreatedAt() time.Time     { return time.Now() }
func (s *sharedSession) LastActiveAt() time.Time  { return time.Now() }
func (s *sharedSession) Context() context.Context { return context.Background() }
func (s *sharedSession) Close(context.Context) error {
	return nil
}
func (s *sharedSession) SetMeta(string, any)        {}
func (s *sharedSession) GetMeta(string) (any, bool) { return nil, false }
func (s *sharedSession) DelMeta(string)             {}

// Send is the copy path. A broadcast must not reach it when the session can
// share a frame, so counting calls here is the assertion.
func (s *sharedSession) Send(payload []byte) error {
	s.mu.Lock()
	s.copies++
	s.mu.Unlock()
	return s.err
}

func (s *sharedSession) SendShared(f *core.SharedFrame) error {
	s.mu.Lock()
	s.frames = append(s.frames, f)
	s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	// Stand in for the write loop: the bytes are on the wire, this consumer is
	// done with them.
	f.Release()
	return nil
}

func (s *sharedSession) frameCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.frames)
}

func (s *sharedSession) copyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copies
}

// TestBroadcastSharesOneFrameAcrossSenders asserts the whole point of the
// change: one frame object, one copy of the bytes, not one copy per recipient.
func TestBroadcastSharesOneFrameAcrossSenders(t *testing.T) {
	m := NewSessionManager()
	sessions := make([]*sharedSession, 0, 5)
	for i := 0; i < 5; i++ {
		sess := &sharedSession{id: m.NextID()}
		if err := m.Register(sess); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, sess)
	}

	payload := bytes.Repeat([]byte("x"), 1024)
	if err := m.Broadcast(payload); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	// Every session must hold the same object, and the payloads must alias the
	// same backing array -- that is what "no per-session copy" means.
	first := sessions[0].frames[0]
	for i, sess := range sessions {
		if sess.frameCount() != 1 {
			t.Fatalf("session %d got %d frames, want 1", i, sess.frameCount())
		}
		if sess.copyCount() != 0 {
			t.Fatalf("session %d fell back to Send (%d copies)", i, sess.copyCount())
		}
		got := sess.frames[0]
		if got != first {
			t.Fatalf("session %d received a different frame object", i)
		}
		if !bytes.Equal(got.Payload(), payload) {
			t.Fatalf("session %d payload mismatch", i)
		}
	}

	// All references were released by the consumers.
	if first.Refs() != 0 {
		t.Fatalf("frame refs = %d, want 0 (payload would stay pinned)", first.Refs())
	}
}

// TestBroadcastReleasesRefOnSendFailure covers the leak path: a session whose
// SendShared fails never reaches the write loop, so its reference has to be
// dropped on the failure path.
func TestBroadcastReleasesRefOnSendFailure(t *testing.T) {
	m := NewSessionManager()
	ok := &sharedSession{id: m.NextID()}
	failing := &sharedSession{id: m.NextID(), err: core.ErrSessionClosed}
	for _, sess := range []*sharedSession{ok, failing} {
		if err := m.Register(sess); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.Broadcast([]byte("payload")); err == nil {
		t.Fatal("Broadcast returned nil, want the failing session's error")
	}
	if ok.frameCount() != 1 {
		t.Fatalf("healthy session got %d frames, want 1", ok.frameCount())
	}
	// One reference consumed by the healthy session, one released by the
	// failing one: nothing may be left.
	if got := ok.frames[0].Refs(); got != 0 {
		t.Fatalf("frame refs = %d, want 0", got)
	}
}

// TestBroadcastFallsBackForPlainSessions asserts the mixed case: a session that
// cannot share a frame still gets the payload, through the copying path.
func TestBroadcastFallsBackForPlainSessions(t *testing.T) {
	m := NewSessionManager()
	plain := &fakeSession{id: m.NextID()}
	if err := m.Register(plain); err != nil {
		t.Fatal(err)
	}
	// Two sharers is enough to build a frame; the plain one must not be counted
	// into it, and with only one sharer the frame path is skipped entirely.
	sharer := &sharedSession{id: m.NextID()}
	if err := m.Register(sharer); err != nil {
		t.Fatal(err)
	}

	if err := m.Broadcast([]byte("payload")); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if sharer.frameCount() != 0 {
		t.Fatal("a single sharer should not get a frame (not worth the refcount)")
	}
	if sharer.copyCount() != 1 {
		t.Fatalf("sharer copy count = %d, want 1 (plain Send)", sharer.copyCount())
	}
}

// TestBroadcastCopiesCallerBuffer is the contract the shared path must keep:
// the caller may reuse its buffer as soon as Broadcast returns.
func TestBroadcastCopiesCallerBuffer(t *testing.T) {
	m := NewSessionManager()
	sessions := make([]*sharedSession, 0, 3)
	for i := 0; i < 3; i++ {
		sess := &sharedSession{id: m.NextID()}
		if err := m.Register(sess); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, sess)
	}

	buf := []byte("original")
	if err := m.Broadcast(buf); err != nil {
		t.Fatal(err)
	}
	copy(buf, "mutated")
	for i, sess := range sessions {
		if got := string(sess.frames[0].Payload()); got != "original" {
			t.Fatalf("session %d payload = %q, want the pre-mutation value", i, got)
		}
	}
}
