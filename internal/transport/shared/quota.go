package shared

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
)

// PeerQuota bounds concurrent admissions globally and per peer address.
//
// It exists because two limits are not the same limit. A global cap alone lets
// one peer take every slot: a UDP flood with forged source addresses, or a
// single host opening connections as fast as the acceptor allows, both pass a
// "max total" check right up to the ceiling and then deny everyone else. The
// per-peer dimension is what turns "the server is full" into "that peer is
// full".
//
// The two dimensions are checked and taken together: TryAcquire either takes a
// slot in both or in neither, so a caller never has to remember which half it
// holds.
//
// Cost: one shard lock plus one CAS on admission, nothing on the data path.
// Callers acquire when a session or connection is created and release when it
// ends, which is why this is not on any per-packet path.
type PeerQuota struct {
	maxTotal int64
	maxPeer  int64
	total    atomic.Int64
	shards   [quotaShards]quotaShard
}

// quotaShards keeps unrelated peers off one another's lock. Sixteen is enough
// that a flood from many addresses does not serialise: the addresses are
// spread by their low bits, and an attacker cannot easily choose addresses that
// collide in one shard while remaining plausibly distinct.
const quotaShards = 16

type quotaShard struct {
	mu    sync.Mutex
	peers map[netip.Addr]int64
}

// NewPeerQuota returns a quota with the given ceilings. Zero or negative means
// unlimited for that dimension, and unlimited on both sides still costs one
// map lookup per admission, so a caller that wants no limit should not build
// one at all.
func NewPeerQuota(maxTotal, maxPeer int64) *PeerQuota {
	return &PeerQuota{maxTotal: maxTotal, maxPeer: maxPeer}
}

func (q *PeerQuota) shard(ip netip.Addr) *quotaShard {
	// Addr.As16 is a cheap value conversion and its last byte varies with the
	// host part; mixing it with the slice length keeps IPv4 and IPv6 peers that
	// share low bytes apart.
	b := ip.As16()
	return &q.shards[(int(b[15])+int(b[14])+int(b[13]))%quotaShards]
}

// QuotaReject says which ceiling refused an admission.
type QuotaReject uint8

const (
	QuotaOK QuotaReject = iota
	// QuotaFull means the global ceiling is reached: the server is full and no
	// peer would be admitted right now.
	QuotaFull
	// QuotaPeerFull means this peer is at its own ceiling while the server as a
	// whole still has room - the case a global cap alone cannot detect.
	QuotaPeerFull
)

// TryAcquire takes one slot for ip, or reports that the quota is exhausted.
func (q *PeerQuota) TryAcquire(ip netip.Addr) bool { return q.Acquire(ip) == QuotaOK }

// Acquire is TryAcquire with the reason for a refusal.
//
// An invalid ip (a peer the transport could not attribute) is limited only by
// the global cap: charging every unattributable admission to one shared bucket
// would cap the whole server at maxPeer, which is worse than the problem being
// solved.
func (q *PeerQuota) Acquire(ip netip.Addr) QuotaReject {
	tracked := ip.IsValid()
	sh := q.shard(ip)
	sh.mu.Lock()
	cur := sh.peers[ip]
	if tracked && q.maxPeer > 0 && cur >= q.maxPeer {
		sh.mu.Unlock()
		return QuotaPeerFull
	}
	// Claim against the global counter while still holding the shard lock, so
	// the per-peer count and the total move together. The CAS loop is the only
	// cross-shard contention left, and it is one atomic on the admission path.
	for {
		total := q.total.Load()
		if q.maxTotal > 0 && total >= q.maxTotal {
			sh.mu.Unlock()
			return QuotaFull
		}
		if q.total.CompareAndSwap(total, total+1) {
			break
		}
	}
	if tracked {
		if sh.peers == nil {
			sh.peers = make(map[netip.Addr]int64)
		}
		sh.peers[ip] = cur + 1
	}
	sh.mu.Unlock()
	return QuotaOK
}

// Release returns a slot taken by TryAcquire.
//
// The per-peer map entry is deleted as soon as its count reaches zero, so a
// flood that cycles through millions of source addresses cannot grow the map:
// the map holds only peers with a live admission. That is why this needs no
// TTL reaper and no background goroutine.
func (q *PeerQuota) Release(ip netip.Addr) {
	if ip.IsValid() {
		sh := q.shard(ip)
		sh.mu.Lock()
		switch cur := sh.peers[ip]; {
		case cur > 1:
			sh.peers[ip] = cur - 1
		case cur == 1:
			delete(sh.peers, ip)
		}
		sh.mu.Unlock()
	}
	q.total.Add(-1)
}

// Total reports how many admissions are currently held.
func (q *PeerQuota) Total() int64 { return q.total.Load() }

// PeerCount reports how many admissions ip currently holds. Intended for tests
// and diagnostics.
func (q *PeerQuota) PeerCount(ip netip.Addr) int64 {
	sh := q.shard(ip)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return sh.peers[ip]
}

// PeerIP extracts the IP from a transport address, in the canonical form the
// quota keys on.
//
// Unmap collapses the IPv4-mapped IPv6 form, so ::ffff:1.2.3.4 and 1.2.3.4 are
// one peer rather than two - the same reason shared.PeerKey normalises, and
// without it a peer could double its allowance simply by using both forms.
//
// An address type it does not recognise yields the zero Addr, which callers
// pass to TryAcquire to get global-only accounting.
func PeerIP(addr net.Addr) netip.Addr {
	switch a := addr.(type) {
	case *net.TCPAddr:
		return normalise(a.AddrPort().Addr(), a.Zone)
	case *net.UDPAddr:
		return normalise(a.AddrPort().Addr(), a.Zone)
	case *net.IPAddr:
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			return netip.Addr{}
		}
		return normalise(ip, a.Zone)
	case nil:
		return netip.Addr{}
	}
	// Fall back to the string form for address types defined outside net
	// (a wrapped listener, a test double). ParseAddrPort then ParseAddr, so
	// both "1.2.3.4:80" and a bare "1.2.3.4" work.
	if ap, err := netip.ParseAddrPort(addr.String()); err == nil {
		return normalise(ap.Addr(), ap.Addr().Zone())
	}
	return PeerIPString(addr.String())
}

// PeerIPString is PeerIP for the transports that only have the string form of
// the remote address (net/http hands out Request.RemoteAddr, not a net.Addr).
//
// It accepts both "1.2.3.4:80" and a bare "1.2.3.4", and returns the zero Addr
// for anything else - a peer that cannot be attributed is limited only by the
// global ceiling, which is the safe direction: guessing would either merge
// unrelated peers into one bucket or invent a bucket per request.
func PeerIPString(remote string) netip.Addr {
	if ap, err := netip.ParseAddrPort(remote); err == nil {
		return normalise(ap.Addr(), "")
	}
	if ip, err := netip.ParseAddr(remote); err == nil {
		return normalise(ip, "")
	}
	return netip.Addr{}
}

func normalise(ip netip.Addr, zone string) netip.Addr {
	if !ip.IsValid() {
		return netip.Addr{}
	}
	ip = ip.Unmap()
	if zone != "" {
		ip = ip.WithZone(zone)
	}
	return ip
}
