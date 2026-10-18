package shared

import (
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPeerQuotaPerPeerAndTotal(t *testing.T) {
	q := NewPeerQuota(3, 2)
	if got := q.Acquire(peerA); got != QuotaOK {
		t.Fatalf("A1 = %v, want ok", got)
	}
	if got := q.Acquire(peerA); got != QuotaOK {
		t.Fatalf("A2 = %v, want ok", got)
	}
	if got := q.Acquire(peerA); got != QuotaPeerFull {
		t.Fatalf("A3 = %v, want peer-full", got)
	}
	if got := q.Acquire(peerB); got != QuotaOK {
		t.Fatalf("B1 = %v, want ok (peer A being full must not close the server)", got)
	}
	if got := q.Acquire(peerB); got != QuotaFull {
		t.Fatalf("B2 = %v, want full: the global ceiling is shared", got)
	}
	if got := q.Total(); got != 3 {
		t.Fatalf("Total = %d, want 3", got)
	}
}

// TestPeerQuotaReleaseDeletesEntry pins the bound on map growth: a quota that
// keeps a zero count per peer grows without limit under a spoofed-source flood,
// which is exactly the traffic it exists to stop.
func TestPeerQuotaReleaseDeletesEntry(t *testing.T) {
	q := NewPeerQuota(100, 1)
	for i := 0; i < 1000; i++ {
		ip := netip.MustParseAddr(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255))
		if got := q.Acquire(ip); got != QuotaOK {
			t.Fatalf("acquire %v = %v", ip, got)
		}
		q.Release(ip)
		if got := q.PeerCount(ip); got != 0 {
			t.Fatalf("PeerCount(%v) = %d after release, want 0", ip, got)
		}
	}
	shard := 0
	for i := range q.shards {
		shard += len(q.shards[i].peers)
	}
	if shard != 0 {
		t.Fatalf("%d peer entries survived release, want 0", shard)
	}
	if got := q.Total(); got != 0 {
		t.Fatalf("Total = %d, want 0", got)
	}
}

// TestPeerQuotaUnattributableIsGlobalOnly: a transport that cannot name the peer
// must not funnel every admission into one per-peer bucket.
func TestPeerQuotaUnattributableIsGlobalOnly(t *testing.T) {
	q := NewPeerQuota(5, 1)
	for i := 0; i < 5; i++ {
		if got := q.Acquire(netip.Addr{}); got != QuotaOK {
			t.Fatalf("unattributable %d = %v, want ok", i, got)
		}
	}
	if got := q.Acquire(netip.Addr{}); got != QuotaFull {
		t.Fatalf("6th = %v, want full", got)
	}
}

func TestPeerQuotaConcurrentDoesNotExceed(t *testing.T) {
	q := NewPeerQuota(8, 3)
	var granted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if q.Acquire(peerA) == QuotaOK {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := granted.Load(); got != 3 {
		t.Fatalf("granted %d, want exactly 3 (the per-peer ceiling)", got)
	}
	if got := q.Total(); got != 3 {
		t.Fatalf("Total = %d, want 3", got)
	}
}

func TestPeerIPNormalises(t *testing.T) {
	cases := []struct {
		name string
		addr net.Addr
		want string
	}{
		{"tcp v4", &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 80}, "203.0.113.7"},
		{"udp v4-mapped", &net.UDPAddr{IP: net.ParseIP("::ffff:203.0.113.7"), Port: 53}, "203.0.113.7"},
		{"tcp v6", &net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 443}, "2001:db8::1"},
		{"ip addr", &net.IPAddr{IP: net.ParseIP("198.51.100.9")}, "198.51.100.9"},
	}
	for _, c := range cases {
		got := PeerIP(c.addr)
		if !got.IsValid() {
			t.Errorf("%s: PeerIP returned an invalid address", c.name)
			continue
		}
		if got.String() != c.want {
			t.Errorf("%s: PeerIP = %s, want %s", c.name, got, c.want)
		}
	}
	// The mapped and plain forms must be one peer, or a peer could double its
	// allowance by using both.
	if PeerIP(&net.UDPAddr{IP: net.ParseIP("::ffff:203.0.113.7"), Port: 1}) != PeerIP(&net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 2}) {
		t.Error("IPv4 and its IPv4-mapped IPv6 form produced different peers")
	}
	if got := PeerIP(nil); got.IsValid() {
		t.Errorf("PeerIP(nil) = %v, want the zero Addr", got)
	}
}
