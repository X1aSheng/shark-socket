package benchmark

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// broadcastSession is a minimal core.Session for the fan-out measurement. It
// exists because the interesting quantity is what Broadcast itself allocates:
// real TCP sessions would add socket writes and dwarf the number under test.
type broadcastSession struct {
	id uint64
	// shared is true when the session takes a reference-counted frame instead
	// of a per-session copy.
	shared bool
	sink   []byte
}

func (s *broadcastSession) ID() uint64                  { return s.id }
func (s *broadcastSession) Protocol() core.Protocol     { return core.ProtocolCustom }
func (s *broadcastSession) RemoteAddr() net.Addr        { return nil }
func (s *broadcastSession) LocalAddr() net.Addr         { return nil }
func (s *broadcastSession) State() core.SessionState    { return core.StateActive }
func (s *broadcastSession) CreatedAt() time.Time        { return time.Now() }
func (s *broadcastSession) LastActiveAt() time.Time     { return time.Now() }
func (s *broadcastSession) Context() context.Context    { return context.Background() }
func (s *broadcastSession) Close(context.Context) error { return nil }
func (s *broadcastSession) SetMeta(string, any)         {}
func (s *broadcastSession) GetMeta(string) (any, bool)  { return nil, false }
func (s *broadcastSession) DelMeta(string)              {}

// Send is what a queued transport does today: copy the payload so the caller
// may release its buffer immediately.
func (s *broadcastSession) Send(payload []byte) error {
	s.sink = make([]byte, len(payload))
	copy(s.sink, payload)
	return nil
}

// SendShared is what a queued transport does with a shared frame: no copy, and
// the reference is dropped once the bytes would have been written.
func (s *broadcastSession) SendShared(f *core.SharedFrame) error {
	f.Release()
	return nil
}

// broadcastManager builds a manager holding n sessions of one kind.
func broadcastManager(n int, shared bool) *runtime.SessionManager {
	m := runtime.NewSessionManager(runtime.WithMaxSessions(int64(n) + 8))
	for i := 0; i < n; i++ {
		sess := &broadcastSession{id: m.NextID(), shared: shared}
		if !shared {
			// Hide SendShared behind a non-shared view so Broadcast has to take
			// the copy path even though the type has the method.
			if err := m.Register(copyOnlySession{sess}); err != nil {
				panic(err)
			}
			continue
		}
		if err := m.Register(sess); err != nil {
			panic(err)
		}
	}
	return m
}

// copyOnlySession wraps a session without exposing SendShared.
type copyOnlySession struct {
	core.Session
}

// BenchmarkBroadcastFanOut compares the two fan-out paths at the same recipient
// count. The shared path should allocate once per broadcast (the frame and its
// single copy of the payload); the copy path allocates once per recipient.
func BenchmarkBroadcastFanOut(b *testing.B) {
	for _, n := range []int{100, 1000} {
		for _, mode := range []struct {
			name   string
			shared bool
		}{
			{"shared", true},
			{"copy", false},
		} {
			b.Run(itoa(n)+"-"+mode.name, func(b *testing.B) {
				m := broadcastManager(n, mode.shared)
				payload := repeatBytes(1024)

				b.ReportAllocs()
				b.SetBytes(int64(len(payload) * n))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if err := m.Broadcast(payload); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
