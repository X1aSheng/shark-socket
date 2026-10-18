package shared

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// ProxyProtocolMode selects whether a listener expects a PROXY protocol header
// ahead of the payload, and which version it accepts.
//
// The header exists for one reason: a TCP connection carries no trace of the
// client that opened it, so everything behind an L4 load balancer sees the
// balancer's address in RemoteAddr. Anything keyed on the peer address (IP
// blacklists, per-IP rate limits, audit logs, TLS session binding) silently
// degrades to "there is only one client" the moment such a balancer is
// deployed. The PROXY protocol is that balancer telling the listener who the
// real peer was, out of band, before the payload.
type ProxyProtocolMode uint8

const (
	// ProxyProtocolOff is the default: the socket peer is the peer.
	ProxyProtocolOff ProxyProtocolMode = iota
	// ProxyProtocolV1 accepts only the CRLF-terminated text header.
	ProxyProtocolV1
	// ProxyProtocolV2 accepts only the binary header.
	ProxyProtocolV2
	// ProxyProtocolAuto accepts either version, distinguished by the prefix.
	// It does not make the header optional: a connection without one is
	// rejected, because a listener that accepts both "with" and "without"
	// lets any client pick its own source address.
	ProxyProtocolAuto
)

func (m ProxyProtocolMode) String() string {
	switch m {
	case ProxyProtocolOff:
		return "off"
	case ProxyProtocolV1:
		return "v1"
	case ProxyProtocolV2:
		return "v2"
	case ProxyProtocolAuto:
		return "auto"
	default:
		return "unknown"
	}
}

var (
	// ErrProxyHeaderInvalid covers a header that is syntactically wrong: bad
	// prefix, wrong field count, unparsable address or port.
	ErrProxyHeaderInvalid = errors.New("shared: invalid PROXY protocol header")
	// ErrProxyHeaderVersion is returned when the header is well formed but of
	// a version the listener was not configured for.
	ErrProxyHeaderVersion = errors.New("shared: PROXY protocol version not accepted by this listener")
	// ErrProxyHeaderTooLong rejects oversized headers instead of buffering
	// whatever the peer sends. v2 allows a 65535-byte header, which is a fine
	// thing to specify and a poor thing to allocate on request.
	ErrProxyHeaderTooLong = errors.New("shared: PROXY protocol header exceeds the size limit")
)

const (
	// proxyV1MaxLen is the largest legal v1 header, including the CRLF.
	proxyV1MaxLen = 107
	// proxyHeaderBuffer bounds both the v1 line scan (ReadSlice returns
	// ErrBufferFull) and the read-ahead that survives into proxyConn, so it is
	// also the buffer the session reads through once the header is consumed.
	proxyHeaderBuffer = 256
	// proxyV2MaxPayload caps the v2 length field. The fixed part of a v2
	// header is at most 216 bytes (UNIX family) plus TLVs; real balancers emit
	// well under 512.
	proxyV2MaxPayload = 2048
	// DefaultProxyHeaderTimeout bounds how long Accept waits for a header. A
	// connection that opens and then says nothing must not hold a slot: this is
	// the same slowloris argument as ReadTimeout, one layer earlier.
	DefaultProxyHeaderTimeout = 5 * time.Second
)

var proxyV1Signature = []byte("PROXY ")

// proxyV2Signature is the 12-byte v2 magic. Its first byte is 0x0D so that a
// v1 text header can never be mistaken for v2, and vice versa.
var proxyV2Signature = [12]byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}

const (
	proxyV2CmdLocal = 0x0
	proxyV2CmdProxy = 0x1

	proxyFamTCP4 = 0x11
	proxyFamUDP4 = 0x12
	proxyFamTCP6 = 0x21
	proxyFamUDP6 = 0x22
)

// ProxyConfig configures a PROXY-protocol listener. The zero value means
// "off", so a caller that leaves the field alone keeps the old behaviour.
type ProxyConfig struct {
	Mode    ProxyProtocolMode
	Timeout time.Duration
	// OnReject, when set, is called for every connection dropped because its
	// header was missing, malformed, late or of the wrong version. Accept
	// cannot return those errors -- a caller such as net/http stops the whole
	// server on the first non-temporary Accept error -- so this is the only
	// place they can be surfaced.
	OnReject func(peer net.Addr, err error)
}

// proxyHeader is the parsed address pair. source is nil when the header
// carries no address (v1 UNKNOWN, v2 LOCAL, or an UNSPEC family), in which
// case the socket peer is the peer.
type proxyHeader struct {
	source net.Addr
	dest   net.Addr
}

// WithRejectHook returns cfg with OnReject set to one that counts and logs
// every rejected connection, delegating to the caller's hook when it already
// set one. It exists so each transport can wire the same observability in one
// line instead of four slightly different copies.
func (cfg ProxyConfig) WithRejectHook(logger core.Logger, metrics core.Metrics) ProxyConfig {
	if cfg.Mode == ProxyProtocolOff {
		return cfg
	}
	userHook := cfg.OnReject
	cfg.OnReject = func(peer net.Addr, err error) {
		if metrics != nil {
			metrics.IncCounter("proxy_header_rejected_total")
		}
		if userHook != nil {
			userHook(peer, err)
			return
		}
		if logger != nil {
			logger.Warn("proxy protocol header rejected", "peer", peer, "error", err)
		}
	}
	return cfg
}

// NewProxyListener wraps ln so that every accepted connection is preceded by a
// PROXY protocol header, and reports the address found there through
// RemoteAddr.
//
// When ProxyConfig.Mode is ProxyProtocolOff the listener is returned
// unchanged, so callers can wrap unconditionally.
//
// Two consequences are worth stating, because they are the failure modes:
//
//   - A connection whose header is absent or invalid is closed and skipped,
//     never passed on unmasked. Accepting it would mean a client could choose
//     whether to be attributed to itself or to an arbitrary address.
//   - The header is read before TLS. The proxy talks to plaintext bytes; it
//     has no way to precede a handshake with an encrypted record.
func NewProxyListener(ln net.Listener, cfg ProxyConfig) net.Listener {
	if ln == nil || cfg.Mode == ProxyProtocolOff {
		return ln
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultProxyHeaderTimeout
	}
	return &proxyListener{Listener: ln, cfg: cfg}
}

type proxyListener struct {
	net.Listener
	cfg ProxyConfig
}

func (l *proxyListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		wrapped, err := l.handshake(conn)
		if err != nil {
			if l.cfg.OnReject != nil {
				l.cfg.OnReject(conn.RemoteAddr(), err)
			}
			_ = conn.Close()
			continue
		}
		return wrapped, nil
	}
}

func (l *proxyListener) handshake(conn net.Conn) (net.Conn, error) {
	if err := conn.SetReadDeadline(time.Now().Add(l.cfg.Timeout)); err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(conn, proxyHeaderBuffer)
	hdr, err := readProxyHeader(br, l.cfg.Mode)
	// Clear the deadline either way: on success the protocol above sets its
	// own per-read deadlines, and leaving this one armed would fire mid-stream.
	// On failure the conn is about to be closed, so the result is irrelevant.
	if derr := conn.SetReadDeadline(time.Time{}); derr != nil && err == nil {
		return nil, derr
	}
	if err != nil {
		return nil, err
	}
	remote := conn.RemoteAddr()
	if hdr.source != nil {
		remote = hdr.source
	}
	return &proxyConn{Conn: conn, r: br, remote: remote}, nil
}

// proxyConn is the accepted connection minus the bytes the header consumed.
// r holds whatever the header parser read past its delimiter, so no payload is
// lost; once r drains, reads go straight to the underlying conn.
type proxyConn struct {
	net.Conn
	r      *bufio.Reader
	remote net.Addr
}

func (c *proxyConn) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *proxyConn) RemoteAddr() net.Addr       { return c.remote }

// CloseWrite forwards the half-close when the underlying conn supports it.
// Without this method a caller doing a FIN-handshake (io.Copy, then CloseWrite)
// would fail its type assertion and fall back to a full Close, truncating the
// peer's in-flight response.
func (c *proxyConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return errors.ErrUnsupported
}

// readProxyHeader consumes exactly one header from br.
//
// Every failure path returns without having read beyond the header, and the
// caller closes the connection, so a partially consumed stream is never
// handed on.
func readProxyHeader(br *bufio.Reader, mode ProxyProtocolMode) (proxyHeader, error) {
	var hdr proxyHeader
	switch mode {
	case ProxyProtocolV1:
		head, err := br.Peek(len(proxyV2Signature))
		if err != nil {
			return hdr, fmt.Errorf("%w: %v", ErrProxyHeaderInvalid, err)
		}
		if !bytes.HasPrefix(head, proxyV1Signature) {
			// A well-formed v2 header is a version mismatch; anything else is
			// not a PROXY header at all, and reporting it as a version problem
			// would send the operator looking at the wrong side.
			if bytes.Equal(head, proxyV2Signature[:]) {
				return hdr, fmt.Errorf("%w: this listener accepts v1 only", ErrProxyHeaderVersion)
			}
			return hdr, fmt.Errorf("%w: missing %q prefix", ErrProxyHeaderInvalid, proxyV1Signature)
		}
		return readProxyV1(br)
	case ProxyProtocolV2:
		head, err := br.Peek(len(proxyV2Signature))
		if err != nil {
			return hdr, fmt.Errorf("%w: %v", ErrProxyHeaderInvalid, err)
		}
		if !bytes.Equal(head, proxyV2Signature[:]) {
			if bytes.HasPrefix(head, proxyV1Signature) {
				return hdr, fmt.Errorf("%w: this listener accepts v2 only", ErrProxyHeaderVersion)
			}
			return hdr, fmt.Errorf("%w: missing v2 signature", ErrProxyHeaderInvalid)
		}
		return readProxyV2(br)
	case ProxyProtocolAuto:
		head, err := br.Peek(len(proxyV2Signature))
		if err != nil {
			return hdr, fmt.Errorf("%w: %v", ErrProxyHeaderInvalid, err)
		}
		if bytes.HasPrefix(head, proxyV1Signature) {
			return readProxyV1(br)
		}
		if bytes.Equal(head, proxyV2Signature[:]) {
			return readProxyV2(br)
		}
		return hdr, fmt.Errorf("%w: no PROXY prefix in either version", ErrProxyHeaderInvalid)
	default:
		return hdr, fmt.Errorf("shared: PROXY protocol mode %d is not a header mode", mode)
	}
}

// readProxyV1 parses the text form:
//
//	PROXY TCP4 192.0.2.1 198.51.100.7 56324 443\r\n
//	PROXY UNKNOWN\r\n
//
// ReadSlice instead of ReadString on purpose: it bounds the scan by the buffer
// size, so a peer that streams bytes without a CRLF cannot grow the allocation.
func readProxyV1(br *bufio.Reader) (proxyHeader, error) {
	var hdr proxyHeader
	line, err := br.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return hdr, fmt.Errorf("%w: v1 line longer than %d bytes", ErrProxyHeaderTooLong, proxyHeaderBuffer)
		}
		return hdr, fmt.Errorf("%w: %v", ErrProxyHeaderInvalid, err)
	}
	if len(line) > proxyV1MaxLen {
		return hdr, fmt.Errorf("%w: v1 line is %d bytes, limit %d", ErrProxyHeaderTooLong, len(line), proxyV1MaxLen)
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return hdr, fmt.Errorf("%w: v1 line is not CRLF terminated", ErrProxyHeaderInvalid)
	}
	fields := strings.Split(string(line[:len(line)-2]), " ")
	if len(fields) < 2 || fields[0] != "PROXY" {
		return hdr, fmt.Errorf("%w: v1 line does not start with PROXY", ErrProxyHeaderInvalid)
	}
	switch fields[1] {
	case "UNKNOWN":
		// The spec allows any trailing content here and requires it to be
		// ignored: it describes a connection the proxy itself originated
		// (health check), whose address is the socket peer.
		return hdr, nil
	case "TCP4", "TCP6":
	default:
		return hdr, fmt.Errorf("%w: v1 family %q", ErrProxyHeaderInvalid, fields[1])
	}
	if len(fields) != 6 {
		return hdr, fmt.Errorf("%w: v1 %s line has %d fields, want 6", ErrProxyHeaderInvalid, fields[1], len(fields))
	}
	src, err := parseProxyIP(fields[2], fields[1] == "TCP4")
	if err != nil {
		return hdr, err
	}
	dst, err := parseProxyIP(fields[3], fields[1] == "TCP4")
	if err != nil {
		return hdr, err
	}
	sport, err := parseProxyPort(fields[4])
	if err != nil {
		return hdr, err
	}
	dport, err := parseProxyPort(fields[5])
	if err != nil {
		return hdr, err
	}
	hdr.source = &net.TCPAddr{IP: src, Port: sport}
	hdr.dest = &net.TCPAddr{IP: dst, Port: dport}
	return hdr, nil
}

func parseProxyIP(s string, wantV4 bool) (net.IP, error) {
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("%w: v1 address %q", ErrProxyHeaderInvalid, s)
	}
	if wantV4 != (ip.To4() != nil) {
		return nil, fmt.Errorf("%w: v1 address %q does not match the declared family", ErrProxyHeaderInvalid, s)
	}
	return ip, nil
}

func parseProxyPort(s string) (int, error) {
	port, err := strconv.Atoi(s)
	if err != nil || port < 0 || port > 65535 {
		return 0, fmt.Errorf("%w: v1 port %q", ErrProxyHeaderInvalid, s)
	}
	return port, nil
}

// readProxyV2 parses the binary form:
//
//	12-byte signature | version+command | family+protocol | u16 length | address block | TLVs
func readProxyV2(br *bufio.Reader) (proxyHeader, error) {
	var hdr proxyHeader
	var fixed [16]byte
	if _, err := io.ReadFull(br, fixed[:]); err != nil {
		return hdr, fmt.Errorf("%w: v2 fixed header: %v", ErrProxyHeaderInvalid, err)
	}
	if !bytes.Equal(fixed[:12], proxyV2Signature[:]) {
		return hdr, fmt.Errorf("%w: v2 signature", ErrProxyHeaderVersion)
	}
	if version := fixed[12] >> 4; version != 0x2 {
		return hdr, fmt.Errorf("%w: v2 version nibble %#x", ErrProxyHeaderVersion, version)
	}
	cmd := fixed[12] & 0x0F
	famProto := fixed[13]
	length := int(binary.BigEndian.Uint16(fixed[14:16]))
	if length > proxyV2MaxPayload {
		return hdr, fmt.Errorf("%w: v2 payload is %d bytes, limit %d", ErrProxyHeaderTooLong, length, proxyV2MaxPayload)
	}
	// The payload is consumed even when it is not needed: the TLVs are part of
	// the header, and leaving them on the stream would feed them to the framer.
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(br, payload); err != nil {
			return hdr, fmt.Errorf("%w: v2 payload: %v", ErrProxyHeaderInvalid, err)
		}
	}
	if cmd != proxyV2CmdProxy {
		// LOCAL: the proxy opened this connection itself, for its own reasons.
		return hdr, nil
	}
	dgram := false
	var srcLen, dstLen int
	switch famProto {
	case proxyFamTCP4, proxyFamUDP4:
		dgram, srcLen, dstLen = famProto == proxyFamUDP4, 4, 4
	case proxyFamTCP6, proxyFamUDP6:
		dgram, srcLen, dstLen = famProto == proxyFamUDP6, 16, 16
	default:
		// AF_UNSPEC or AF_UNIX: no address to report, fall back to the socket.
		return hdr, nil
	}
	if length < srcLen+dstLen+4 {
		return hdr, fmt.Errorf("%w: v2 address block is %d bytes, want %d",
			ErrProxyHeaderInvalid, length, srcLen+dstLen+4)
	}
	srcIP := make(net.IP, srcLen)
	copy(srcIP, payload[:srcLen])
	dstIP := make(net.IP, dstLen)
	copy(dstIP, payload[srcLen:srcLen+dstLen])
	sport := int(binary.BigEndian.Uint16(payload[srcLen+dstLen:]))
	dport := int(binary.BigEndian.Uint16(payload[srcLen+dstLen+2:]))
	hdr.source = proxyAddr(srcIP, sport, dgram)
	hdr.dest = proxyAddr(dstIP, dport, dgram)
	return hdr, nil
}

func proxyAddr(ip net.IP, port int, dgram bool) net.Addr {
	if dgram {
		return &net.UDPAddr{IP: ip, Port: port}
	}
	return &net.TCPAddr{IP: ip, Port: port}
}
