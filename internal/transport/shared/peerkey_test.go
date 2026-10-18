package shared

import (
	"net"
	"testing"
)

func TestPeerKeyDistinguishesPeers(t *testing.T) {
	cases := []struct {
		name string
		a    *net.UDPAddr
		b    *net.UDPAddr
	}{
		{"port", &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 1}, &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 2}},
		{"address", &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 1}, &net.UDPAddr{IP: net.ParseIP("203.0.113.8"), Port: 1}},
		{"v4 and v6", &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 1}, &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 1}},
		{"zone", &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: 1, Zone: "eth0"}, &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: 1, Zone: "eth1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if NewPeerKey(tc.a) == NewPeerKey(tc.b) {
				t.Errorf("NewPeerKey(%v) collides with NewPeerKey(%v)", tc.a, tc.b)
			}
		})
	}
}

// TestPeerKeyNormalisesIPv4Mapped pins the reason addr is stored in 16-byte
// form: a peer that reaches us over a v4-mapped socket must map to the same
// session as the same peer over a plain v4 socket. If To16 normalisation were
// dropped, one peer could hold two sessions and double its resource ceiling.
func TestPeerKeyNormalisesIPv4Mapped(t *testing.T) {
	plain := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 5683}
	mapped := &net.UDPAddr{IP: net.ParseIP("::ffff:203.0.113.7"), Port: 5683}
	if NewPeerKey(plain) != NewPeerKey(mapped) {
		t.Errorf("v4 and v4-mapped forms of one peer produced different keys: %v vs %v",
			NewPeerKey(plain), NewPeerKey(mapped))
	}
}

// TestPeerKeyStringMatchesUDPAddr keeps the rendered form identical to what
// net.UDPAddr.String produced before the switch, so log lines, observer
// registries and metrics labels do not silently change shape.
func TestPeerKeyStringMatchesUDPAddr(t *testing.T) {
	addrs := []*net.UDPAddr{
		{IP: net.ParseIP("203.0.113.7"), Port: 5683},
		{IP: net.ParseIP("2001:db8::1"), Port: 5683},
		{IP: net.ParseIP("fe80::1"), Port: 1, Zone: "eth0"},
		{IP: net.ParseIP("::ffff:203.0.113.7"), Port: 1},
	}
	for _, addr := range addrs {
		if got, want := NewPeerKey(addr).String(), addr.String(); got != want {
			t.Errorf("PeerKey.String() = %q, want net.UDPAddr.String() %q", got, want)
		}
	}
}

func TestPeerKeyNilAddr(t *testing.T) {
	// A malformed endpoint must not panic inside the read loop.
	if k := NewPeerKey(nil); k != (PeerKey{}) {
		t.Errorf("NewPeerKey(nil) = %v, want the zero key", k)
	}
}

func TestPeerKeyAllocFree(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 5683}
	if allocs := testing.AllocsPerRun(200, func() { _ = NewPeerKey(addr) }); allocs != 0 {
		t.Errorf("NewPeerKey allocates %v objects, want 0", allocs)
	}
}

func TestPeerKeyUDPAddrRoundTrip(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 5683, Zone: "eth0"}
	back := NewPeerKey(addr).UDPAddr()
	if !back.IP.Equal(addr.IP) || back.Port != addr.Port || back.Zone != addr.Zone {
		t.Errorf("UDPAddr() = %v, want %v", back, addr)
	}
}
