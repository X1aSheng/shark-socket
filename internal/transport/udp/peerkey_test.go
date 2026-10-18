package udp

import (
	"net"
	"sync"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
)

// BenchmarkUDPSessionLookup measures the per-datagram cost of resolving a peer
// endpoint to its session - the work every single datagram pays before the
// payload ever reaches a plugin. The interesting numbers are allocs/op and
// B/op: ns/op on this path is dominated by the sync.Map lookup, while the
// allocations are pure garbage per datagram.
func BenchmarkUDPSessionLookup(b *testing.B) {
	s := &Server{}
	addr := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 4242}
	key := shared.NewPeerKey(addr)
	sess := &session{id: 1, remote: addr, key: key}
	sess.state.Store(uint32(core.StateActive))
	s.sessions.Store(key, sess)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := s.getOrCreateSession(addr); got != sess {
			b.Fatalf("getOrCreateSession() = %v, want the stored session", got)
		}
	}
}

// BenchmarkUDPAddrStringKey is the retained baseline: the pre-ADR-015 hot path,
// reproduced directly against a sync.Map so the delta stays measurable instead
// of turning into an unverifiable claim.
func BenchmarkUDPAddrStringKey(b *testing.B) {
	var m sync.Map
	addr := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 4242}
	sess := &session{id: 1, remote: addr}
	m.Store(addr.String(), sess)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, ok := m.Load(addr.String())
		if !ok || v.(*session) != sess {
			b.Fatal("lookup missed the stored session")
		}
	}
}

// TestUDPSessionLookupAllocs pins the allocation behaviour of the hot path.
// getOrCreateSession runs once per datagram, so any allocation here is
// multiplied by the packet rate and ends up as pure GC pressure.
func TestUDPSessionLookupAllocs(t *testing.T) {
	s := &Server{}
	addr := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 4242}
	key := shared.NewPeerKey(addr)
	sess := &session{id: 1, remote: addr, key: key}
	sess.state.Store(uint32(core.StateActive))
	s.sessions.Store(key, sess)

	allocs := testing.AllocsPerRun(200, func() {
		if got := s.getOrCreateSession(addr); got != sess {
			t.Fatalf("getOrCreateSession() = %v, want the stored session", got)
		}
	})
	t.Logf("getOrCreateSession allocs/op = %v", allocs)
	if allocs != 0 {
		t.Errorf("getOrCreateSession allocates %v objects per datagram, want 0", allocs)
	}
}
