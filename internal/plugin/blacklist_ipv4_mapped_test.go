package plugin

import (
	"net"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// TestBlacklistIPv4MappedExactIP verifies that an IPv4-mapped IPv6 remote
// address ("::ffff:10.0.0.1", as reported by dual-stack sockets on some
// platforms) cannot bypass an exact-match IPv4 entry.
func TestBlacklistIPv4MappedExactIP(t *testing.T) {
	p := NewBlacklist("10.0.0.1")

	v4 := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1234}}
	if err := p.OnAccept(v4); err != core.ErrPluginBlock {
		t.Fatalf("plain IPv4 error = %v, want %v", err, core.ErrPluginBlock)
	}

	mapped := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("::ffff:10.0.0.1"), Port: 1234}}
	if err := p.OnAccept(mapped); err != core.ErrPluginBlock {
		t.Fatalf("IPv4-mapped error = %v, want %v (mapped address bypassed the exact entry)", err, core.ErrPluginBlock)
	}

	// Non-blocked addresses still pass.
	other := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 1234}}
	if err := p.OnAccept(other); err != nil {
		t.Fatalf("unrelated address error = %v, want nil", err)
	}
}

// TestBlacklistIPv4MappedEntry verifies that an entry given in mapped form
// also blocks plain IPv4 peers (normalization applies to both sides).
func TestBlacklistIPv4MappedEntry(t *testing.T) {
	p := NewBlacklist("::ffff:192.168.0.7")
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("192.168.0.7"), Port: 1}}
	if err := p.OnAccept(sess); err != core.ErrPluginBlock {
		t.Fatalf("error = %v, want %v", err, core.ErrPluginBlock)
	}
}
