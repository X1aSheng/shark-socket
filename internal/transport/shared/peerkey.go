package shared

import (
	"net"
	"strconv"
)

// PeerKey is a comparable, allocation-free identity for a datagram peer.
//
// Datagram transports (UDP, CoAP) resolve every single packet to a session by
// its peer endpoint. Using addr.String() as the map key costs two allocations
// per packet inside net.UDPAddr.String (one for IP.String, one for
// JoinHostPort) plus a third to box the resulting string into the map's
// interface{} key - garbage that scales with the packet rate and is discarded
// immediately after the lookup.
//
// PeerKey is a plain comparable struct, so building one allocates nothing. It
// is still boxed once when handed to a sync.Map, which is why the conversion is
// done once per session instead of once per packet wherever the key can be
// cached on the session.
type PeerKey struct {
	// addr always holds the 16-byte form so that an IPv4 address reached as
	// "::ffff:1.2.3.4" and the same peer reached as "1.2.3.4" produce one
	// key, not two sessions for one peer.
	addr [16]byte
	port uint16
	zone string
}

// NewPeerKey builds the session key for a UDP peer. A nil address yields the
// zero key rather than panicking, so a malformed endpoint can never take the
// read loop down.
func NewPeerKey(addr *net.UDPAddr) PeerKey {
	if addr == nil {
		return PeerKey{}
	}
	var k PeerKey
	// To16 normalises both the 4-byte and the 16-byte net.IP forms; it
	// returns nil for an address that is neither, leaving the zero key.
	copy(k.addr[:], addr.IP.To16())
	k.port = uint16(addr.Port)
	k.zone = addr.Zone
	return k
}

// String renders the endpoint in the same form net.UDPAddr.String produces, so
// log lines, observer registries and metrics labels keep their existing shape.
// It allocates, so it belongs on the cold path (logs, observer keys) only.
func (k PeerKey) String() string {
	ip := net.IP(k.addr[:])
	if k.zone != "" {
		return net.JoinHostPort(ip.String()+"%"+k.zone, strconv.Itoa(int(k.port)))
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(k.port)))
}

// UDPAddr reconstructs a net.UDPAddr for writing back to the peer.
func (k PeerKey) UDPAddr() *net.UDPAddr {
	return &net.UDPAddr{IP: net.IP(k.addr[:]), Port: int(k.port), Zone: k.zone}
}
