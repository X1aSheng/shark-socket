package shared

import (
	"net"
	"net/netip"
	"sync"
)

// GateOptions tunes a GateListener. Both hooks are optional.
type GateOptions struct {
	// Reject runs on the raw connection before it is closed, so a protocol that
	// can say "not now" (HTTP) may answer 503 instead of vanishing. It must not
	// block: it runs on the accept goroutine.
	Reject func(conn net.Conn, reason RejectReason)
	// OnReject runs for every refusal, counted or logged here. It also runs on
	// the accept goroutine.
	OnReject func(reason RejectReason)
}

// NewGateListener wraps ln so that every accepted connection is admitted
// against acc and released when it closes.
//
// It exists for the transports that have no accept loop of their own. net/http
// serves straight off a listener, so the natural place to bound it is the
// listener - by the time a ConnState hook runs, the file descriptor and the
// goroutine have already been spent, which is exactly the resource the ceiling
// is meant to protect.
//
// The wrapper must sit *below* any TLS listener, not above it: net/http
// type-asserts its connection to *tls.Conn to drive the handshake, and a
// wrapper in between would make every HTTPS connection fail. Below TLS the
// returned conn is plaintext, which is also why a Reject hook here cannot speak
// HTTP to a TLS client and should be left nil in that case.
func NewGateListener(ln net.Listener, acc *Acceptor, opts GateOptions) net.Listener {
	if ln == nil || acc == nil {
		return ln
	}
	return &gateListener{Listener: ln, acc: acc, opts: opts}
}

type gateListener struct {
	net.Listener
	acc  *Acceptor
	opts GateOptions
}

func (l *gateListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		peer := PeerIP(conn.RemoteAddr())
		if reason := l.acc.TryAccept(peer); reason != AcceptOK {
			// Count first, answer second: the peer can observe the refusal as
			// soon as Reject writes to the socket, and a metric that lands
			// afterwards races whoever is reading it.
			if l.opts.OnReject != nil {
				l.opts.OnReject(reason)
			}
			if l.opts.Reject != nil {
				l.opts.Reject(conn, reason)
			}
			_ = conn.Close()
			continue
		}
		return &gatedConn{Conn: conn, acc: l.acc, peer: peer}, nil
	}
}

// Active reports the number of connections currently admitted through the
// listener.
func (l *gateListener) Active() int64 { return l.acc.Active() }

// gatedConn releases its slot exactly once. Close is the only release point:
// net/http and the transport layers call it on every exit path, and a
// sync.Once makes a double Close (there are several in net/http) harmless.
type gatedConn struct {
	net.Conn
	acc     *Acceptor
	peer    netip.Addr
	release sync.Once
}

func (c *gatedConn) Close() error {
	c.release.Do(func() { c.acc.Done(c.peer) })
	return c.Conn.Close()
}
