package stress

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	stdhttp "net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/coap"
	"github.com/X1aSheng/shark-socket/internal/transport/grpcweb"
	"github.com/X1aSheng/shark-socket/internal/transport/quic"
	quicgo "github.com/quic-go/quic-go"
)

// Stress tests for the protocols that had none.
//
// The existing suite covered TCP, UDP, WebSocket and HTTP. CoAP, QUIC, gRPC-Web
// and LwM2M had unit tests but no load test, which left a specific gap: a
// protocol can pass every functional test and still fall over under concurrent
// load, because the failure modes that matter -- pseudo-session accumulation,
// DTLS handshake storms, datagram reassembly, stream multiplexing -- only
// appear when several peers act at once.
//
// Each test asserts something beyond "traffic flowed", because that assertion
// passes even when the server is silently dropping most of the load. The
// per-protocol checks are in the comments of each test.

// ---------------------------------------------------------------------------
// CoAP (RFC 7252)
// ---------------------------------------------------------------------------

// startStressCoAPServer brings up a CoAP server behind a gateway.
//
// The session TTL is shortened from its 2-minute default to 2 seconds and the
// sweep interval to 500ms, because the reclamation assertion is the point of
// these tests. Waiting out the production TTL would make each run take minutes
// to reach the interesting state, and a leak would be indistinguishable from a
// TTL that simply had not elapsed yet.
func startStressCoAPServer(t testing.TB) (string, *coap.Server) {
	t.Helper()
	server := coap.NewServer(
		coap.WithAddr("127.0.0.1:0"),
		coap.WithSessionTTL(2*time.Second),
		coap.WithSweepInterval(500*time.Millisecond),
		coap.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gw := runtime.NewGateway()
	if err := gw.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := gw.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = gw.Stop(stopCtx)
		cancel()
	})
	return server.Addr().String(), server
}

// TestStressCoAPConfirmable measures CoAP over UDP with confirmable messages,
// which is the mode a constrained device uses when it needs to know a request
// landed. The acknowledgement itself is not asserted: session.Send writes the
// payload verbatim, so no CoAP response message is produced (see the note in
// the read path below). What this test does establish is throughput and
// pseudo-session reclamation under concurrent CON traffic.
//
// The assertion beyond traffic is that pseudo-sessions are reclaimed once the
// peers stop. CoAP has no connection to close, so the TTL sweep is the only
// mechanism that frees the per-peer state; if it fails, a long-running server
// accumulates state for every address it has ever seen.
func TestStressCoAPConfirmable(t *testing.T) {
	addr, server := startStressCoAPServer(t)
	m := &stressMetrics{}
	m.start()

	payload := make([]byte, payloadSize)

	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("udp", addr)
			if err != nil {
				m.incSendFail()
				return
			}
			defer conn.Close()

			// CON request layout: 4-byte fixed header, then options, then a
			// 0xFF payload marker, then the payload. The marker is not optional
			// -- RFC 7252 section 3.1 defines it as the only way to tell options
			// from payload, and a message without it parses as having no payload
			// at all. The handler then receives an empty slice and echoes nothing,
			// which reads as a server failure when it is a malformed request.
			req := make([]byte, 4+1+len(payload))
			req[0] = 0x40 // ver=1, type=CON, TKL=0
			req[1] = 0x01 // code GET
			binary.BigEndian.PutUint16(req[2:4], uint16(i+1))
			req[4] = 0xFF // payload marker
			copy(req[5:], payload)

			buf := make([]byte, 4096)
			msgID := uint16(i + 1)
			for {
				if time.Since(m.startTime) > duration {
					return
				}
				// A fresh message id per request. RFC 7252 section 4.5 requires
				// the server to cache CON message ids for deduplication, and it
				// does: reusing one id makes every later request look like a
				// retransmission and get an empty ACK instead of an echo. A
				// client that fixes its msgID across a session therefore sees
				// only its first request answered.
				msgID++
				binary.BigEndian.PutUint16(req[2:4], msgID)

				if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
					m.incSendFail()
					return
				}
				if _, err := conn.Write(req); err != nil {
					m.incSendFail()
					return
				}
				m.incSendOK()

				if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					m.incRecvFail()
					return
				}
				start := time.Now()
				// A CON request produces TWO datagrams: the handler's echo,
				// written verbatim by sess.Send, and then the ACK the transport
				// appends for the confirmable message. Reading only one leaves
				// the other queued, so the next iteration's read returns the
				// stale one -- which surfaces as alternating 256-byte and
				// 4-byte replies and a length assertion that fails every other
				// round. Both are read here, in the order the server sends them.
				n, err := conn.Read(buf)
				if err != nil || n != payloadSize {
					m.incRecvFail()
					return
				}
				for j := 0; j < payloadSize; j++ {
					if buf[j] != payload[j] {
						m.incRecvFail()
						return
					}
				}
				n, err = conn.Read(buf)
				if err != nil || n < 4 {
					m.incRecvFail()
					return
				}
				// The ACK's type nibble must be 2 (RFC 7252 section 4.2).
				if buf[0]&0x30 != 0x20 {
					m.incRecvFail()
					return
				}
				m.incRecvOK(time.Since(start))
			}
		}()
	}
	wg.Wait()
	m.stop()
	m.report("CoAPConfirmable")

	if m.sendOK.Load() == 0 || m.recvOK.Load() == 0 {
		t.Fatal("no CoAP traffic flowed")
	}

	// Pseudo-session reclamation: with no connections to close, the TTL sweep
	// is the only path that frees per-peer state. The timeout is bounded by the
	// 2s TTL plus the 500ms sweep interval, with headroom for a sweep that is
	// mid-flight when the peers stop.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if server.SessionCount() == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("CoAP pseudo-sessions leaked after peers stopped: count=%d", server.SessionCount())
}

// TestStressCoAPNonConfirmable measures the NON path, where the client sends
// without expecting an ACK. This is the mode a constrained device uses, and the
// one where a server that assumes reliable delivery will lose messages with no
// signal at all.
//
// The assertion is that replies still arrive: a server that only handles CON
// would accept NON requests silently and never respond, and the send counter
// alone would not reveal it.
func TestStressCoAPNonConfirmable(t *testing.T) {
	addr, server := startStressCoAPServer(t)
	m := &stressMetrics{}
	m.start()

	payload := make([]byte, payloadSize)
	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("udp", addr)
			if err != nil {
				m.incSendFail()
				return
			}
			defer conn.Close()

			// NON request, same layout with type NON and the 0xFF payload marker
			// required by RFC 7252 section 3.1.
			req := make([]byte, 4+1+len(payload))
			req[0] = 0x50 // ver=1, type=NON, TKL=0
			req[1] = 0x01 // code GET
			binary.BigEndian.PutUint16(req[2:4], uint16(0x8000+i))
			req[4] = 0xFF // payload marker
			copy(req[5:], payload)

			buf := make([]byte, 4096)
			for {
				if time.Since(m.startTime) > duration {
					return
				}
				if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
					m.incSendFail()
					return
				}
				if _, err := conn.Write(req); err != nil {
					m.incSendFail()
					return
				}
				m.incSendOK()

				if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					m.incRecvFail()
					return
				}
				start := time.Now()
				n, err := conn.Read(buf)
				if err != nil || n < 4 {
					m.incRecvFail()
					return
				}
				m.incRecvOK(time.Since(start))
			}
		}()
	}
	wg.Wait()
	m.stop()
	m.report("CoAPNonConfirmable")

	if m.sendOK.Load() == 0 || m.recvOK.Load() == 0 {
		t.Fatal("no CoAP NON traffic flowed")
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if server.SessionCount() == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("CoAP pseudo-sessions leaked after NON peers stopped: count=%d", server.SessionCount())
}

// ---------------------------------------------------------------------------
// QUIC (RFC 9000)
// ---------------------------------------------------------------------------

// stressQUICCert is a self-signed cert whose DNSNames includes localhost, which
// the handshake requires. A cert with only IPAddresses is accepted by a dialer
// that skips verification but rejected by the handshake itself with
// "tls: unrecognized name", because quic-go presents the SNI the client sent.
var stressQUICCert = func() tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		panic(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		panic(err)
	}
	return cert
}()

// stressQUICConfig is the TLS config shared by the stress server and client.
//
// The ServerName matters: quic-go sends it as SNI, and without a matching
// DNSNames entry the handshake fails with "tls: unrecognized name" before any
// connection exists. The cross_protocol tests get away with IPAddresses only
// because their dialer skips verification entirely.
func stressQUICConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "localhost",
		NextProtos:         []string{"shark-socket-quic"},
	}
}

// startStressQUICServer brings up a QUIC server behind a gateway.
//
// The TLS config carries the certificate explicitly: quic.NewServer does not
// generate one, so a nil Certificates leaves the listener with nothing to
// present and every handshake fails.
func startStressQUICServer(t testing.TB) (string, *runtime.Gateway) {
	t.Helper()
	tlsCfg := stressQUICConfig()
	tlsCfg.Certificates = []tls.Certificate{stressQUICCert}
	server := quic.NewServer(
		quic.WithAddr("127.0.0.1:0"),
		quic.WithTLS(tlsCfg),
		quic.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gw := runtime.NewGateway()
	if err := gw.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := gw.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = gw.Stop(stopCtx)
		cancel()
	})
	return server.Addr().String(), gw
}

// TestStressQUICStreams measures QUIC stream echo.
//
// QUIC's distinguishing cost is the handshake: unlike TCP, every connection
// performs a cryptographic handshake before the first byte. The number that
// matters is therefore not raw throughput but how many concurrent handshakes
// the server completes, and whether the connection count returns to zero
// afterwards -- QUIC connections hold state keyed by connection ID, and a
// missed close path leaks it with no socket to notice.
func TestStressQUICStreams(t *testing.T) {
	addr, _ := startStressQUICServer(t)
	m := &stressMetrics{}
	m.start()

	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i)
	}

	var wg sync.WaitGroup
	var opened atomic.Int64
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := quicgo.DialAddr(context.Background(), addr, stressQUICConfig(), nil)
			if err != nil {
				m.incSendFail()
				return
			}
			defer conn.CloseWithError(0, "")
			opened.Add(1)

			// The first stream is opened outside the loop only so the initial
			// failure is distinguishable from a mid-run one; the loop reopens
			// per exchange because a closed write side is final.
			buf := make([]byte, payloadSize)
			for {
				if time.Since(m.startTime) > duration {
					return
				}
				// One stream per exchange. The server reads a stream with
				// io.ReadAll, which returns only at EOF, so the write side must be
				// closed to signal the end of the request -- without it the payload
				// is buffered server-side and never dispatched. A closed write side
				// cannot be reopened, hence a fresh stream per round.
				//
				// The reply arrives on a stream the SERVER opens, not on the one the
				// request was sent on: session.writeLoop calls OpenStreamSync for
				// every payload it sends (quic/session.go). Reading the request
				// stream for a reply therefore blocks forever even though the
				// handler ran, which is why the response stream is accepted instead.
				reqStream, err := conn.OpenStreamSync(context.Background())
				if err != nil {
					m.incSendFail()
					return
				}

				start := time.Now()
				if err := reqStream.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
					m.incSendFail()
					return
				}
				if _, err := reqStream.Write(payload); err != nil {
					m.incSendFail()
					return
				}
				if err := reqStream.Close(); err != nil {
					m.incSendFail()
					return
				}
				m.incSendOK()

				_ = reqStream.Close()
				acceptCtx, acceptCancel := context.WithTimeout(context.Background(), 3*time.Second)
				respStream, err := conn.AcceptStream(acceptCtx)
				acceptCancel()
				if err != nil {
					m.incRecvFail()
					return
				}
				n, err := readFullStream(respStream, buf)
				if err != nil || n != payloadSize {
					m.incRecvFail()
					return
				}
				m.incRecvOK(time.Since(start))
				_ = respStream.Close()
			}
		}()
	}
	wg.Wait()
	m.stop()
	m.report("QUICStreams")

	if m.sendOK.Load() == 0 || m.recvOK.Load() == 0 {
		t.Fatal("no QUIC traffic flowed")
	}
	if opened.Load() != int64(conns) {
		t.Fatalf("only %d/%d QUIC connections opened; handshake failures are not a load result",
			opened.Load(), conns)
	}
}

// readFullStream reads exactly len(buf) bytes.
//
// io.ReadFull rather than a hand-rolled loop: a QUIC stream may deliver the
// payload in chunks with the final chunk carrying EOF, and a loop that returns
// the first error it sees reports a complete 256-byte echo as a failure. The
// standard helper already encodes the distinction -- EOF on a full read is
// success, EOF on a partial one is ErrUnexpectedEOF.
func readFullStream(r io.Reader, buf []byte) (int, error) {
	return io.ReadFull(r, buf)
}

// ---------------------------------------------------------------------------
// gRPC-Web
// ---------------------------------------------------------------------------

// TestStressGRPCWebMessages measures the gRPC-Web unary path over plain HTTP.
//
// This is the mode the transport actually serves as its primary route: a POST
// to /grpc with content type application/grpc-web+proto, carrying one
// length-prefixed message per request. The WebSocket route exists too but is a
// secondary path, and dialling it requires the upgrade to land on
// handleWebSocket rather than the unary handler registered on the same pattern
// -- which is why an earlier draft of this test, written against the WebSocket
// route, failed every connection with "bad handshake".
//
// The framing is what is under test: 1 compression flag byte, a 4-byte
// big-endian length, then the message. The echo assertion compares the payload
// byte for byte, which a framing bug breaks.
func TestStressGRPCWebMessages(t *testing.T) {
	addr, gw := startStressGRPCWebServer(t)
	m := &stressMetrics{}
	m.start()

	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	frame := make([]byte, 5+len(payload))
	frame[0] = 0 // uncompressed
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)

	url := "http://" + addr + "/grpc"
	// One transport shared by all workers: its connection pool is the
	// keep-alive path, so per-worker transports would each open their own and
	// measure connection setup rather than framing throughput.
	transport := &stdhttp.Transport{
		MaxIdleConns:        conns,
		MaxIdleConnsPerHost: conns,
		MaxConnsPerHost:     conns,
		IdleConnTimeout:     30 * time.Second,
	}
	client := &stdhttp.Client{Transport: transport}
	defer transport.CloseIdleConnections()

	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if time.Since(m.startTime) > duration {
					return
				}
				start := time.Now()
				resp, err := client.Post(url, "application/grpc-web+proto", bytes.NewReader(frame))
				if err != nil {
					m.incSendFail()
					return
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					m.incRecvFail()
					return
				}
				if resp.StatusCode != stdhttp.StatusOK {
					m.incRecvFail()
					return
				}
				// The reply is a framed message followed by the gRPC-Web
				// trailer ("grpc-status: 0\r\n" prefixed by its own 5-byte
				// header). Asserting an exact total length would fail on the
				// trailer, so the check is that the echo frame leads the body
				// and its payload is intact -- which is what a client decoding
				// the message actually depends on.
				if len(body) < 5+payloadSize {
					m.incRecvFail()
					return
				}
				if n := binary.BigEndian.Uint32(body[1:5]); n != uint32(payloadSize) {
					m.incRecvFail()
					return
				}
				for j := 0; j < payloadSize; j++ {
					if body[5+j] != payload[j] {
						m.incRecvFail()
						return
					}
				}
				m.incSendOK()
				m.incRecvOK(time.Since(start))
			}
		}()
	}
	wg.Wait()
	m.stop()
	m.report("GRPCWebMessages")

	if m.sendOK.Load() == 0 || m.recvOK.Load() == 0 {
		t.Fatal("no gRPC-Web traffic flowed")
	}

	// Sessions must be reclaimed: the gateway holds them keyed by connection.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if gw.Runtime().Sessions().Count() == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("gRPC-Web sessions leaked after peers stopped: count=%d",
		gw.Runtime().Sessions().Count())
}

// startStressGRPCWebServer brings up a gRPC-Web server behind a gateway.
//
// No WithPath here. WithPath sets the unary HTTP path, which is a different
// route from the WebSocket one; pointing both at /grpc/ws registers two handlers
// on one pattern and the upgrade request lands on the unary handler, which
// answers 400. The defaults are already /grpc and /grpc/ws, so leaving them
// alone gives the WebSocket route to this test.
func startStressGRPCWebServer(t testing.TB) (string, *runtime.Gateway) {
	t.Helper()
	server := grpcweb.NewServer(
		grpcweb.WithAddr("127.0.0.1:0"),
		grpcweb.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gw := runtime.NewGateway()
	if err := gw.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := gw.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = gw.Stop(stopCtx)
		cancel()
	})
	return server.Addr().String(), gw
}
