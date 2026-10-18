// Command protocolgen is a multi-protocol load generator.
//
// Every capacity number so far came from TCP echo, which leaves the question
// "which protocol gives up first at the same offered load" unanswered. Each
// protocol here speaks its own handshake, framing and shutdown, so the generator
// holds one client per protocol rather than one generic dialer.
//
// Usage:
//
//	protocolgen -protocol udp  -host 10.0.0.1:18002 -conns 50 -rate 40000 -duration 10s
//	protocolgen -protocol coap -host 10.0.0.1:18005 -conns 50 -rate 40000 -duration 10s
//
// The -protocol values are: tcp, udp, ws, http, coap, grpcweb, quic.
//
// Framing notes, each of which cost real debugging time and is easy to get
// wrong when reading only the client code:
//
//   - tcp: raw bytes, no framing. Used as the reference point.
//   - udp: one datagram per message; a reply that arrives with a different
//     length is a real failure, not a short read.
//   - ws: client frames must be masked (RFC 6455 5.3). An unmasked frame is a
//     protocol error and the server answers close(1000).
//   - http: unary POST with keep-alive; the transport pins one connection pool
//     so the measurement is request handling rather than connection setup.
//   - coap: the 0xFF payload marker is mandatory (RFC 7252 3.1) or the payload
//     parses as absent. A CON request produces two datagrams (echo + ACK), so
//     both are read or the next iteration reads a stale one.
//   - grpcweb: a frame is 1 flag byte + 4-byte big-endian length + message; the
//     reply is followed by a trailer frame.
//   - quic: the server reads with io.ReadAll, so the request stream must be
//     half-closed or the payload is never dispatched; and the reply arrives on
//     a stream the SERVER opens, which must be accepted rather than read.
package main

import (
	"bytes"
	"context"

	"crypto/tls"

	"encoding/binary"

	"flag"
	"fmt"
	"io"
	"log"

	"net"
	stdhttp "net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/scripts/ladmetrics"
	gws "github.com/gorilla/websocket"
	quicgo "github.com/quic-go/quic-go"
)

var (
	flagHost     = flag.String("host", "127.0.0.1:18000", "server host:port")
	flagProtocol = flag.String("protocol", "tcp", "tcp | udp | ws | http | coap | grpcweb | quic")
	flagConns    = flag.Int("conns", 50, "concurrent connections")
	flagRate     = flag.Int("rate", 10000, "target messages per second across all connections")
	flagDuration = flag.Duration("duration", 10*time.Second, "run length")
	flagSize     = flag.Int("size", 256, "payload bytes")
	flagLabel    = flag.String("label", "", "label echoed in the summary")
	flagPath     = flag.String("path", "", "path for ws (/ws) and http (/grpc)")
	flagAlpn     = flag.String("alpn", "shark-socket-quic", "QUIC ALPN token")
)

// ptr returns a pointer to a copy of s, for the first-failure slot.
func ptr(s string) *string { return &s }

func main() {
	flag.Parse()

	conns := *flagConns
	dur := *flagDuration
	size := *flagSize

	dial, err := dialerFor(*flagProtocol)
	if err != nil {
		log.Fatal(err)
	}

	// Batching mirrors rateclient: a ticker whose period is a few microseconds
	// cannot keep up and silently caps the achieved rate, so send a batch per
	// tick and keep rate/conns/batch above the ticker's practical floor.
	perConn := *flagRate / conns
	if perConn < 1 {
		perConn = 1
	}
	batch := perConn / 20
	if batch < 1 {
		batch = 1
	}
	effective := perConn / batch
	if effective < 1 {
		effective = 1
	}
	interval := time.Second / time.Duration(effective)

	coll := ladmetrics.NewCollector(conns)
	deadline := time.Now().Add(dur)
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i)
	}

	// firstErr records why the run produced nothing, when it did. A generator
	// that reports only a counter leaves "the tool is broken" and "the server
	// refused every connection" looking identical from the outside, and those
	// need completely different investigations.
	var firstErr atomic.Pointer[string]

	var workers sync.WaitGroup
	for i := range conns {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			session, err := dial(*flagHost)
			if err != nil {
				coll.NoteError()
				firstErr.CompareAndSwap(nil, ptr(fmt.Sprintf("dial: %v", err)))
				return
			}
			defer session.Close()

			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			msgID := uint16(id + 1)

			for range ticker.C {
				if time.Now().After(deadline) {
					return
				}
				for range batch {
					msgID++
					n, err := session.Exchange(coll.Conn(id), payload, msgID)
					if err != nil {
						coll.NoteError()
						firstErr.CompareAndSwap(nil,
							ptr(fmt.Sprintf("exchange: %v", err)))
						// A protocol error repeats on every tick; give up on
						// this connection so one bad peer cannot flood the
						// error counter for the whole run.
						return
					}
					if n != size {
						coll.NoteError()
						firstErr.CompareAndSwap(nil,
							ptr(fmt.Sprintf("short reply: got %d bytes, want %d", n, size)))
						return
					}
					coll.NoteSend()
					coll.RecordEcho(id)
				}
			}
		}(i)
	}

	// Sample counters on their own goroutine so sampling cadence does not
	// perturb the send cadence.
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		coll.Collect(int(dur.Seconds()) + 3)
	}()

	// The workers stop on the deadline; readers may still be draining, so wait
	// past it before summarising or the final counts are short.
	workers.Wait()
	time.Sleep(100 * time.Millisecond)
	sampler.Wait()

	rep := coll.Summarize(*flagLabel, *flagProtocol, conns, size, float64(*flagRate), dur)
	fmt.Print(rep.String())
	if rep.Recv == 0 {
		if msg := firstErr.Load(); msg != nil {
			fmt.Printf("first_failure=%s\n", *msg)
		} else {
			fmt.Println("first_failure=(none recorded)")
		}
		os.Exit(1)
	}
}

// session is one open connection to the server under test.
type session interface {
	// Exchange sends one payload and returns the echoed byte count. It calls
	// markSent and recordEcho itself so latency is measured per protocol where
	// the semantics differ (a CoAP CON yields two datagrams, for instance).
	Exchange(conn *ladmetrics.Conn, payload []byte, msgID uint16) (int, error)
	Close()
}

// dialerFor returns the session constructor for a protocol.
func dialerFor(protocol string) (func(string) (session, error), error) {
	switch strings.ToLower(protocol) {
	case "tcp":
		return dialTCP, nil
	case "udp":
		return dialUDP, nil
	case "ws":
		return dialWS, nil
	case "http":
		return dialHTTP, nil
	case "coap":
		return dialCoAP, nil
	case "grpcweb":
		return dialGRPCWeb, nil
	case "quic":
		return dialQUIC, nil
	default:
		return nil, fmt.Errorf("unknown protocol %q", protocol)
	}
}

// --- TCP ---

type tcpSession struct{ conn net.Conn }

func dialTCP(addr string) (session, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &tcpSession{conn: c}, nil
}

// Exchange sends one length-prefixed frame and reads the echo.
//
// The framing is not optional: the transport uses LengthPrefixFramer, a 4-byte
// big-endian length followed by the payload, and the server rejects anything it
// cannot parse. Writing the bare payload means the server reads the first four
// bytes as a length -- with a sequential payload that is 0x00010203, far past
// MaxFrameBytes, so the connection is dropped with no echo.
//
// Worth noting because it produces a false pass in the other direction: an
// all-zero payload declares length zero, the server replies with a zero header,
// and a client that parses the header the same way sees a perfect echo forever
// while never exchanging a single payload byte.
func (s *tcpSession) Exchange(conn *ladmetrics.Conn, payload []byte, _ uint16) (int, error) {
	// Both directions get a deadline. Without one, a saturated server that
	// stops replying leaves this Read blocked forever, so an 8-second rung
	// turns into an indefinite hang instead of a reported stall -- which is
	// exactly what the rung exists to detect.
	if err := s.conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}

	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))

	conn.MarkSent()
	if _, err := s.conn.Write(hdr[:]); err != nil {
		return 0, err
	}
	if _, err := s.conn.Write(payload); err != nil {
		return 0, err
	}

	if _, err := io.ReadFull(s.conn, hdr[:]); err != nil {
		return 0, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n != uint32(len(payload)) {
		return 0, fmt.Errorf("echo length %d, want %d", n, len(payload))
	}
	buf := make([]byte, n)
	got, err := io.ReadFull(s.conn, buf)
	if err != nil {
		return got, err
	}
	if !bytes.Equal(buf, payload) {
		return got, fmt.Errorf("echo payload differs from sent payload")
	}
	return got, nil
}

func (s *tcpSession) Close() { _ = s.conn.Close() }

// --- UDP ---

type udpSession struct{ conn net.Conn }

func dialUDP(addr string) (session, error) {
	c, err := net.DialTimeout("udp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &udpSession{conn: c}, nil
}

// Exchange measures one datagram. io.ReadFull rather than Read because a short
// read is a lost byte, not a slow arrival, and treating them alike would report
// a silent truncation as a timeout.
func (s *udpSession) Exchange(conn *ladmetrics.Conn, payload []byte, _ uint16) (int, error) {
	if err := s.conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}
	conn.MarkSent()
	if _, err := s.conn.Write(payload); err != nil {
		return 0, err
	}
	buf := make([]byte, 65535)
	return s.conn.Read(buf)
}

func (s *udpSession) Close() { _ = s.conn.Close() }

// --- WebSocket ---

type wsSession struct{ conn *gws.Conn }

func dialWS(addr string) (session, error) {
	path := *flagPath
	if path == "" {
		path = "/ws"
	}
	d := gws.Dialer{HandshakeTimeout: 5 * time.Second}
	// The Origin header is sent deliberately. A server that leaves CheckOrigin
	// unset falls back to gorilla's same-origin comparison, which has nothing
	// to compare when the client sends no Origin at all -- and a real browser
	// always sends one, so omitting it would make the generator the least
	// representative client in the suite.
	hdr := stdhttp.Header{}
	hdr.Set("Origin", "http://"+addr)
	c, _, err := d.Dial("ws://"+addr+path, hdr)
	if err != nil {
		return nil, err
	}
	return &wsSession{conn: c}, nil
}

func (s *wsSession) Exchange(conn *ladmetrics.Conn, payload []byte, _ uint16) (int, error) {
	if err := s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}
	if err := s.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}
	conn.MarkSent()
	if err := s.conn.WriteMessage(gws.BinaryMessage, payload); err != nil {
		return 0, err
	}
	_, data, err := s.conn.ReadMessage()
	return len(data), err
}

func (s *wsSession) Close() { _ = s.conn.Close() }

// --- HTTP (gRPC-Web unary, one pooled transport) ---

var httpTransport *stdhttp.Transport
var httpClient *stdhttp.Client

func dialHTTP(addr string) (session, error) {
	if httpTransport == nil {
		// One transport for every worker so the measurement is request handling
		// rather than connection setup: per-worker transports would each open
		// their own connection and drown the frame handling in TCP handshakes.
		httpTransport = &stdhttp.Transport{
			MaxIdleConns:        *flagConns,
			MaxIdleConnsPerHost: *flagConns,
			MaxConnsPerHost:     *flagConns,
			IdleConnTimeout:     30 * time.Second,
		}
		httpClient = &stdhttp.Client{Transport: httpTransport}
	}
	path := *flagPath
	if path == "" {
		path = "/grpc"
	}
	// The response is checked at run level; a session here is just an endpoint.
	return &httpSession{url: "http://" + addr + path}, nil
}

type httpSession struct{ url string }

func (s *httpSession) Exchange(conn *ladmetrics.Conn, payload []byte, _ uint16) (int, error) {
	frame := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)

	conn.MarkSent()
	resp, err := httpClient.Post(s.url, "application/grpc-web+proto", bytes.NewReader(frame))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != stdhttp.StatusOK {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	if len(body) < 5 {
		return 0, io.ErrUnexpectedEOF
	}
	// The reply is the framed echo followed by a trailer, so the payload is at
	// body[5:5+n].
	n := int(binary.BigEndian.Uint32(body[1:5]))
	if 5+n > len(body) || n != len(payload) {
		return 0, io.ErrUnexpectedEOF
	}
	return n, nil
}

func (s *httpSession) Close() {}

// --- CoAP ---

type coapSession struct{ conn net.Conn }

func dialCoAP(addr string) (session, error) {
	c, err := net.DialTimeout("udp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &coapSession{conn: c}, nil
}

// Exchange sends a NON request.
//
// NON rather than CON deliberately: CON requires reading two datagrams (the echo
// and the ACK) and reusing message ids safely, which makes the generator
// harder to read without changing what is being measured -- queueing behaviour
// is identical. CON throughput is measured by tests/stress, which asserts the
// two-datagram contract properly.
//
// Layout: 4-byte fixed header, 0xFF payload marker, payload. The marker is
// mandatory; without it the server parses the message as having no payload.
func (s *coapSession) Exchange(conn *ladmetrics.Conn, payload []byte, msgID uint16) (int, error) {
	req := make([]byte, 5+len(payload))
	req[0] = 0x50 // ver=1, type=NON, TKL=0
	req[1] = 0x01 // code GET
	binary.BigEndian.PutUint16(req[2:4], msgID)
	req[4] = 0xFF // payload marker
	copy(req[5:], payload)

	if err := s.conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}
	conn.MarkSent()
	if _, err := s.conn.Write(req); err != nil {
		return 0, err
	}
	buf := make([]byte, 65535)
	return s.conn.Read(buf)
}

func (s *coapSession) Close() { _ = s.conn.Close() }

// --- gRPC-Web over WebSocket ---

type grpcWSSession struct{ conn net.Conn }

func dialGRPCWeb(addr string) (session, error) {
	path := *flagPath
	if path == "" {
		path = "/grpc/ws"
	}
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if err := grpcWSHandshake(c, addr, path); err != nil {
		_ = c.Close()
		return nil, err
	}
	return &grpcWSSession{conn: c}, nil
}

// grpcWSHandshake performs the RFC 6455 upgrade by hand. The key must be
// base64 of exactly 16 bytes or a conforming server answers 400.
func grpcWSHandshake(conn net.Conn, addr, path string) error {
	const key = "dGhlIHNhbXBsZSBub25jZQ=="
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Origin: http://" + addr + "\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return err
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}
	if !strings.Contains(string(buf[:n]), "101") {
		return fmt.Errorf("grpc-web websocket handshake failed: %q",
			strings.SplitN(string(buf[:n]), "\r\n", 2)[0])
	}
	return nil
}

func (s *grpcWSSession) Exchange(conn *ladmetrics.Conn, payload []byte, _ uint16) (int, error) {
	frame := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)

	conn.MarkSent()
	if err := s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}
	if _, err := s.conn.Write(maskFrame(0x2, frame)); err != nil {
		return 0, err
	}
	// Read a WebSocket message, not raw bytes: the session sends with
	// WriteMessage(BinaryMessage, ...), so the payload arrives inside a frame
	// and reading straight off the socket consumes the frame header as the
	// first bytes of the length prefix, which then never completes.
	if err := s.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}
	_, data, err := wsReadFrame(s.conn)
	if err != nil {
		return 0, err
	}
	got := int(binary.BigEndian.Uint32(data[1:5]))
	if 5+got > len(data) || got != len(payload) {
		return 0, io.ErrUnexpectedEOF
	}
	return got, nil
}

func (s *grpcWSSession) Close() { _ = s.conn.Close() }

// wsReadFrame reads one unmasked server frame: a 2-byte header, an extended
// length when the short form is not enough, then the payload.
//
// Server frames must not be masked (RFC 6455 5.1), so the mask bit is checked
// rather than handled -- a set bit means something is wrong on the far end and
// silently ignoring it would misalign every subsequent read.
func wsReadFrame(c net.Conn) (byte, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return 0, nil, err
	}
	opcode := hdr[0] & 0x0f
	if hdr[1]&0x80 != 0 {
		return opcode, nil, fmt.Errorf("server frame carries a mask bit (RFC 6455 5.1)")
	}
	length := uint64(hdr[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c, ext[:]); err != nil {
			return opcode, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c, ext[:]); err != nil {
			return opcode, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c, buf); err != nil {
		return opcode, nil, err
	}
	return opcode, buf, nil
}

// --- QUIC ---

type quicSession struct{ conn *quicgo.Conn }

func dialQUIC(addr string) (session, error) {
	cfg := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "localhost",
		NextProtos:         []string{*flagAlpn},
	}
	c, err := quicgo.DialAddr(context.Background(), addr, cfg, nil)
	if err != nil {
		return nil, err
	}
	return &quicSession{conn: c}, nil
}

func (s *quicSession) Exchange(conn *ladmetrics.Conn, payload []byte, _ uint16) (int, error) {
	conn.MarkSent()
	reqStream, err := s.conn.OpenStreamSync(context.Background())
	if err != nil {
		return 0, err
	}
	if _, err := reqStream.Write(payload); err != nil {
		_ = reqStream.Close()
		return 0, err
	}
	// Half-close, or the server's io.ReadAll never returns and the handler is
	// never reached.
	if err := reqStream.Close(); err != nil {
		return 0, err
	}

	// The reply arrives on a stream the SERVER opened (session.writeLoop calls
	// OpenStreamSync per payload), so it has to be accepted, not read here.
	acceptCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	respStream, err := s.conn.AcceptStream(acceptCtx)
	if err != nil {
		return 0, err
	}
	defer respStream.Close()
	if err := respStream.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}
	buf := make([]byte, len(payload))
	return io.ReadFull(respStream, buf)
}

func (s *quicSession) Close() { _ = s.conn.CloseWithError(0, "") }

// --- helpers ---

// maskFrame builds a client frame with the mask bit set.
//
// RFC 6455 section 5.3 requires every client frame to be masked and a server to
// reject one that is not. Centralising the encoding keeps that rule in one place
// instead of repeating it per protocol, where it is easy to forget.
//
// The extended length forms matter more than they look: a gRPC-Web message of
// 256 bytes plus its 5-byte header is 261, and byte(261) is 5. Writing that
// truncated value declares a 5-byte payload, the server reads five bytes and
// treats the remaining 256 as the next frame, and every later read is
// misaligned. The symptom -- an occasional unexpected EOF at one exact payload
// size -- looks nothing like a framing bug, which is why it is worth spelling
// out rather than fixing quietly.
func maskFrame(opcode byte, payload []byte) []byte {
	var out []byte
	out = append(out, 0x80|opcode)
	n := len(payload)
	switch {
	case n <= 125:
		out = append(out, 0x80|byte(n))
	case n <= 0xFFFF:
		// 126 marks a 16-bit length that follows.
		out = append(out, 0x80|126, byte(n>>8), byte(n))
	default:
		// 127 marks a 64-bit length.
		out = append(out, 0x80|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		out = append(out, ext[:]...)
	}
	mask := [4]byte{0x01, 0x02, 0x03, 0x04}
	out = append(out, mask[:]...)
	masked := make([]byte, n)
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	return append(out, masked...)
}
