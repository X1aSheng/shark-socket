package benchmark_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/X1aSheng/shark-socket/internal/gateway"
	httpproto "github.com/X1aSheng/shark-socket/internal/protocol/http"
	tcpproto "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
	"github.com/X1aSheng/shark-socket/internal/protocol/udp"
	wsproto "github.com/X1aSheng/shark-socket/internal/protocol/websocket"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func waitTCPReady(b *testing.B, addr string) {
	b.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	b.Fatalf("TCP server at %s not ready", addr)
}

// retryDial dials with retries to handle Windows ephemeral port exhaustion.
func retryDial(network, addr string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout(network, addr, 2*time.Second)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitUDPReady(b *testing.B, addr string) {
	b.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("udp", addr, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.Fatalf("UDP server at %s not ready", addr)
}

// retryGet performs HTTP GET with retries for Windows ephemeral port exhaustion.
func retryGet(client *http.Client, url string, timeout time.Duration) (*http.Response, error) {
	deadline := time.Now().Add(timeout)
	for {
		resp, err := client.Get(url)
		if err == nil {
			return resp, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// retryPost performs HTTP POST with retries for Windows ephemeral port exhaustion.
func retryPost(client *http.Client, url, contentType string, body io.Reader, timeout time.Duration) (*http.Response, error) {
	deadline := time.Now().Add(timeout)
	for {
		resp, err := client.Post(url, contentType, body)
		if err == nil {
			return resp, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// makePayload generates a deterministic payload of the given size.
func makePayload(size int) []byte {
	p := make([]byte, size)
	for i := range p {
		p[i] = byte(i % 256)
	}
	return p
}

// verifyPayload checks that got matches expected byte-for-byte.
func verifyPayload(got, expected []byte) bool {
	if len(got) != len(expected) {
		return false
	}
	for i := range got {
		if got[i] != expected[i] {
			return false
		}
	}
	return true
}

// tcpWriteFrame writes a length-prefixed frame to conn.
func tcpWriteFrame(conn net.Conn, payload []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))
	_, err := conn.Write(append(header, payload...))
	return err
}

// tcpReadFrame reads a length-prefixed frame from conn.
func tcpReadFrame(conn net.Conn) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint32(header))
	payload := make([]byte, size)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// safeFramer creates a per-read bufio.Reader to avoid shared state across sessions.
type safeFramer struct{ maxSize int }

func (f *safeFramer) ReadFrame(r io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint32(header))
	if f.maxSize > 0 && size > f.maxSize {
		return nil, io.ErrUnexpectedEOF
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (f *safeFramer) WriteFrame(w io.Writer, payload []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))
	_, err := w.Write(append(header, payload...))
	return err
}

// startTCPServer creates, starts, and returns a TCP echo server on a random port.
// Caller must call stop() when done.
func startTCPServer(b *testing.B, opts ...tcpproto.Option) (srv *tcpproto.Server, addr string) {
	b.Helper()
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	allOpts := append([]tcpproto.Option{
		tcpproto.WithAddr("127.0.0.1", 0),
		tcpproto.WithWorkerPool(4, 16, 256),
		tcpproto.WithWriteQueueSize(256),
	}, opts...)
	srv = tcpproto.NewServer(handler, allOpts...)
	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	addr = srv.Addr().String()
	waitTCPReady(b, addr)
	return srv, addr
}

// stopServer stops a server with a 5s timeout.
func stopServer(srv interface{ Stop(context.Context) error }) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Stop(ctx)
}

// ---------------------------------------------------------------------------
// TCP Multi-Size Payload Echo Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkTCPEcho_MultiSize(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"64B", 64},
		{"256B", 256},
		{"1KB", 1024},
		{"4KB", 4096},
		{"16KB", 16384},
		{"64KB", 65536},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			srv, addr := startTCPServer(b,
				tcpproto.WithFramer(&safeFramer{maxSize: sz.size + 1024}),
			)
			defer stopServer(srv)

			conn, err := retryDial("tcp", addr, 10*time.Second)
			if err != nil {
				b.Fatalf("dial: %v", err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(120 * time.Second))

			payload := makePayload(sz.size)

			// Warm up
			if err := tcpWriteFrame(conn, payload); err != nil {
				b.Fatalf("warmup write: %v", err)
			}
			got, err := tcpReadFrame(conn)
			if err != nil {
				b.Fatalf("warmup read: %v", err)
			}
			if !verifyPayload(got, payload) {
				b.Fatalf("warmup integrity check failed: size %d", sz.size)
			}

			b.ReportAllocs()
			b.SetBytes(int64(sz.size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if err := tcpWriteFrame(conn, payload); err != nil {
					b.Fatalf("write: %v", err)
				}
				got, err := tcpReadFrame(conn)
				if err != nil {
					b.Fatalf("read: %v", err)
				}
				if !verifyPayload(got, payload) {
					b.Fatalf("integrity mismatch at size %d: got %d bytes", sz.size, len(got))
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TCP Binary Integrity Verification
// ---------------------------------------------------------------------------

func BenchmarkTCPEcho_BinaryIntegrity(b *testing.B) {
	srv, addr := startTCPServer(b,
		tcpproto.WithFramer(&safeFramer{maxSize: 65536}),
	)
	defer stopServer(srv)

	conn, err := retryDial("tcp", addr, 10*time.Second)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(120 * time.Second))

	patterns := []struct {
		name    string
		payload []byte
	}{
		{"AllZero", make([]byte, 1024)},
		{"AllFF", func() []byte { p := make([]byte, 1024); for i := range p { p[i] = 0xFF }; return p }()},
		{"Ascending", makePayload(1024)},
		{"Descending", func() []byte { p := make([]byte, 1024); for i := range p { p[i] = byte(255 - i%256) }; return p }()},
		{"RandomLike", func() []byte { p := make([]byte, 1024); for i := range p { p[i] = byte(i*7 + i*i*3) }; return p }()},
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		pat := patterns[i%len(patterns)]
		if err := tcpWriteFrame(conn, pat.payload); err != nil {
			b.Fatalf("write: %v", err)
		}
		got, err := tcpReadFrame(conn)
		if err != nil {
			b.Fatalf("read: %v", err)
		}
		if !verifyPayload(got, pat.payload) {
			b.Fatalf("binary integrity failed for pattern %s at iter %d", pat.name, i)
		}
	}
}

// ---------------------------------------------------------------------------
// TCP Parallel Multi-Client Throughput
// ---------------------------------------------------------------------------

func BenchmarkTCPEcho_ParallelMultiClient(b *testing.B) {
	srv, addr := startTCPServer(b,
		tcpproto.WithWorkerPool(8, 32, 1024),
		tcpproto.WithFullPolicy(tcpproto.PolicyBlock),
		tcpproto.WithWriteQueueSize(512),
		tcpproto.WithFramer(&safeFramer{maxSize: 8192}),
	)
	defer stopServer(srv)

	payload := makePayload(256)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		conn, err := retryDial("tcp", addr, 10*time.Second)
		if err != nil {
			b.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(120 * time.Second))

		for pb.Next() {
			if err := tcpWriteFrame(conn, payload); err != nil {
				b.Fatalf("write: %v", err)
			}
			got, err := tcpReadFrame(conn)
			if err != nil {
				b.Fatalf("read: %v", err)
			}
			if !verifyPayload(got, payload) {
				b.Fatalf("integrity mismatch in parallel client")
			}
		}
	})
}

// ---------------------------------------------------------------------------
// TCP Burst Traffic
// ---------------------------------------------------------------------------

func BenchmarkTCPEcho_Burst(b *testing.B) {
	srv, addr := startTCPServer(b,
		tcpproto.WithWorkerPool(8, 32, 1024),
		tcpproto.WithWriteQueueSize(2048),
		tcpproto.WithFramer(&safeFramer{maxSize: 8192}),
	)
	defer stopServer(srv)

	conn, err := retryDial("tcp", addr, 10*time.Second)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(120 * time.Second))

	burstSize := 50
	payload := makePayload(512)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for j := 0; j < burstSize; j++ {
			if err := tcpWriteFrame(conn, payload); err != nil {
				b.Fatalf("burst write %d: %v", j, err)
			}
		}
		for j := 0; j < burstSize; j++ {
			got, err := tcpReadFrame(conn)
			if err != nil {
				b.Fatalf("burst read %d: %v", j, err)
			}
			if !verifyPayload(got, payload) {
				b.Fatalf("burst integrity failed at msg %d", j)
			}
		}
	}
	b.ReportMetric(float64(burstSize), "burst_size")
}

// ---------------------------------------------------------------------------
// TCP PingPong — strict request/response with sequence integrity
// ---------------------------------------------------------------------------

func BenchmarkTCPEcho_PingPong(b *testing.B) {
	srv, addr := startTCPServer(b,
		tcpproto.WithFramer(&safeFramer{maxSize: 8192}),
	)
	defer stopServer(srv)

	conn, err := retryDial("tcp", addr, 10*time.Second)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(120 * time.Second))

	payload := make([]byte, 8)

	// Warm up
	tcpWriteFrame(conn, payload)
	tcpReadFrame(conn)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binary.BigEndian.PutUint64(payload, uint64(i))
		if err := tcpWriteFrame(conn, payload); err != nil {
			b.Fatalf("write: %v", err)
		}
		got, err := tcpReadFrame(conn)
		if err != nil {
			b.Fatalf("read: %v", err)
		}
		seq := binary.BigEndian.Uint64(got[:8])
		if seq != uint64(i) {
			b.Fatalf("integrity violation: got seq %d, want %d", seq, i)
		}
	}
}

// ---------------------------------------------------------------------------
// UDP Multi-Size Payload Echo Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkUDPEcho_MultiSize(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"64B", 64},
		{"256B", 256},
		{"512B", 512},
		{"1KB", 1024},
		{"1400B", 1400},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			handler := func(sess types.RawSession, msg types.RawMessage) error {
				return sess.Send(msg.Payload)
			}
			srv := udp.NewServer(handler, udp.WithAddr("127.0.0.1", 0))
			if err := srv.Start(); err != nil {
				b.Fatalf("Start: %v", err)
			}
			defer stopServer(srv)

			srvAddr := srv.Addr().String()
			waitUDPReady(b, srvAddr)

			conn, err := net.DialUDP("udp", nil, srv.Addr().(*net.UDPAddr))
			if err != nil {
				b.Fatalf("DialUDP: %v", err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(120 * time.Second))

			payload := makePayload(sz.size)
			buf := make([]byte, 1500)

			// Warm up
			conn.Write(payload)
			n, err := conn.Read(buf)
			if err != nil {
				b.Fatalf("warmup read: %v", err)
			}
			if !verifyPayload(buf[:n], payload) {
				b.Fatalf("warmup integrity check failed")
			}

			b.ReportAllocs()
			b.SetBytes(int64(sz.size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				conn.Write(payload)
				n, err := conn.Read(buf)
				if err != nil {
					b.Fatalf("Read: %v", err)
				}
				if !verifyPayload(buf[:n], payload) {
					b.Fatalf("UDP integrity mismatch: got %d bytes, want %d", n, sz.size)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WebSocket Echo Benchmark
// ---------------------------------------------------------------------------

func BenchmarkWSEcho(b *testing.B) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := wsproto.NewServer(handler,
		wsproto.WithAddr("127.0.0.1", 0),
		wsproto.WithPath("/ws"),
		wsproto.WithMaxMessageSize(65536),
		wsproto.WithAllowedOrigins("*"),
	)
	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer stopServer(srv)

	wsURL := fmt.Sprintf("ws://%s/ws", srv.Addr())
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		b.Fatalf("Dial: %v", err)
	}
	defer wsConn.Close()
	wsConn.SetReadDeadline(time.Now().Add(120 * time.Second))

	payload := makePayload(256)

	// Warm up
	wsConn.WriteMessage(websocket.BinaryMessage, payload)
	_, got, err := wsConn.ReadMessage()
	if err != nil {
		b.Fatalf("warmup read: %v", err)
	}
	if !verifyPayload(got, payload) {
		b.Fatalf("warmup integrity failed")
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := wsConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			b.Fatalf("write: %v", err)
		}
		_, got, err := wsConn.ReadMessage()
		if err != nil {
			b.Fatalf("read: %v", err)
		}
		if !verifyPayload(got, payload) {
			b.Fatalf("WS integrity mismatch: got %d bytes, want %d", len(got), len(payload))
		}
	}
}

// ---------------------------------------------------------------------------
// WebSocket Multi-Size Echo Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkWSEcho_MultiSize(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"64B", 64},
		{"256B", 256},
		{"1KB", 1024},
		{"4KB", 4096},
		{"16KB", 16384},
		{"64KB", 65536},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			handler := func(sess types.RawSession, msg types.RawMessage) error {
				return sess.Send(msg.Payload)
			}
			srv := wsproto.NewServer(handler,
				wsproto.WithAddr("127.0.0.1", 0),
				wsproto.WithPath("/ws"),
				wsproto.WithMaxMessageSize(sz.size+1024),
				wsproto.WithAllowedOrigins("*"),
			)
			if err := srv.Start(); err != nil {
				b.Fatalf("Start: %v", err)
			}
			defer stopServer(srv)

			wsURL := fmt.Sprintf("ws://%s/ws", srv.Addr())
			wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				b.Fatalf("Dial: %v", err)
			}
			defer wsConn.Close()
			wsConn.SetReadDeadline(time.Now().Add(120 * time.Second))

			payload := makePayload(sz.size)

			// Warm up
			wsConn.WriteMessage(websocket.BinaryMessage, payload)
			_, got, err := wsConn.ReadMessage()
			if err != nil {
				b.Fatalf("warmup read: %v", err)
			}
			if !verifyPayload(got, payload) {
				b.Fatalf("warmup integrity failed")
			}

			b.ReportAllocs()
			b.SetBytes(int64(sz.size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if err := wsConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
					b.Fatalf("write: %v", err)
				}
				_, got, err := wsConn.ReadMessage()
				if err != nil {
					b.Fatalf("read: %v", err)
				}
				if !verifyPayload(got, payload) {
					b.Fatalf("WS integrity mismatch at size %d", sz.size)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WebSocket Parallel Multi-Client
// ---------------------------------------------------------------------------

func BenchmarkWSEcho_Parallel(b *testing.B) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := wsproto.NewServer(handler,
		wsproto.WithAddr("127.0.0.1", 0),
		wsproto.WithPath("/ws"),
		wsproto.WithMaxMessageSize(4096),
		wsproto.WithMaxSessions(1000),
		wsproto.WithAllowedOrigins("*"),
	)
	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer stopServer(srv)

	wsURL := fmt.Sprintf("ws://%s/ws", srv.Addr())
	payload := makePayload(128)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			b.Fatalf("Dial: %v", err)
		}
		defer wsConn.Close()
		wsConn.SetReadDeadline(time.Now().Add(120 * time.Second))

		for pb.Next() {
			if err := wsConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
				b.Fatalf("write: %v", err)
			}
			_, got, err := wsConn.ReadMessage()
			if err != nil {
				b.Fatalf("read: %v", err)
			}
			if !verifyPayload(got, payload) {
				b.Fatalf("WS parallel integrity mismatch")
			}
		}
	})
}

// ---------------------------------------------------------------------------
// HTTP Echo Benchmark
// ---------------------------------------------------------------------------

func BenchmarkHTTPEcho(b *testing.B) {
	srv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", 0))
	srv.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte(r.URL.Query().Get("data")))
	})

	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer stopServer(srv)

	addr := srv.Addr().String()
	waitTCPReady(b, addr)

	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("http://%s/echo?data=benchmark-http-echo-test", addr)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := retryGet(client, url, 60*time.Second)
		if err != nil {
			b.Fatalf("GET: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("status = %d", resp.StatusCode)
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP Request/Response Throughput with Body
// ---------------------------------------------------------------------------

func BenchmarkHTTP_Throughput(b *testing.B) {
	srv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", 0))
	srv.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		cl := r.ContentLength
		if cl > 0 {
			w.Header().Set("X-Echo-Size", fmt.Sprintf("%d", cl))
		}
		buf := make([]byte, 4096)
		for {
			_, err := r.Body.Read(buf)
			if err != nil {
				break
			}
		}
		r.Body.Close()
		w.Write([]byte("ok"))
	})

	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer stopServer(srv)

	addr := srv.Addr().String()
	waitTCPReady(b, addr)

	url := fmt.Sprintf("http://%s/upload", addr)
	payload := makePayload(1024)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := retryPost(http.DefaultClient, url, "application/octet-stream", strings.NewReader(string(payload)), 60*time.Second)
		if err != nil {
			b.Fatalf("POST: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("status = %d", resp.StatusCode)
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP Concurrent Requests
// ---------------------------------------------------------------------------

func BenchmarkHTTP_Concurrent(b *testing.B) {
	srv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", 0))
	srv.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	})

	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer stopServer(srv)

	addr := srv.Addr().String()
	waitTCPReady(b, addr)

	url := fmt.Sprintf("http://%s/ping", addr)
	// Shared client with connection pooling to avoid ephemeral port exhaustion
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 64,
			IdleConnTimeout:    90 * time.Second,
		},
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := retryGet(client, url, 60*time.Second)
			if err != nil {
				b.Fatalf("GET: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				b.Fatalf("status = %d", resp.StatusCode)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Cross-Protocol Gateway Throughput
// ---------------------------------------------------------------------------

func BenchmarkGateway_MultiProtocol(b *testing.B) {
	echoHandler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}

	tcpSrv := tcpproto.NewServer(echoHandler,
		tcpproto.WithAddr("127.0.0.1", 0),
		tcpproto.WithWorkerPool(4, 16, 256),
		tcpproto.WithFramer(&safeFramer{maxSize: 8192}),
	)

	wsSrv := wsproto.NewServer(echoHandler,
		wsproto.WithAddr("127.0.0.1", 0),
		wsproto.WithPath("/ws"),
		wsproto.WithMaxMessageSize(8192),
		wsproto.WithAllowedOrigins("*"),
	)

	httpSrv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", 0))
	httpSrv.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("gateway-http"))
	})

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(tcpSrv)
	gw.Register(wsSrv)
	gw.Register(httpSrv)

	if err := gw.Start(); err != nil {
		b.Fatalf("gateway Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	tcpAddr := tcpSrv.Addr().String()
	wsAddr := wsSrv.Addr().String()
	httpAddr := httpSrv.Addr().String()

	waitTCPReady(b, tcpAddr)
	waitTCPReady(b, wsAddr)
	waitTCPReady(b, httpAddr)

	payload := makePayload(256)

	b.Run("TCP", func(b *testing.B) {
		conn, err := retryDial("tcp", tcpAddr, 10*time.Second)
		if err != nil {
			b.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(120 * time.Second))

		b.ReportAllocs()
		b.SetBytes(int64(len(payload)))
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			if err := tcpWriteFrame(conn, payload); err != nil {
				b.Fatalf("write: %v", err)
			}
			got, err := tcpReadFrame(conn)
			if err != nil {
				b.Fatalf("read: %v", err)
			}
			if !verifyPayload(got, payload) {
				b.Fatalf("TCP integrity mismatch")
			}
		}
	})

	b.Run("WebSocket", func(b *testing.B) {
		wsURL := fmt.Sprintf("ws://%s/ws", wsAddr)
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			b.Fatalf("Dial: %v", err)
		}
		defer wsConn.Close()
		wsConn.SetReadDeadline(time.Now().Add(120 * time.Second))

		b.ReportAllocs()
		b.SetBytes(int64(len(payload)))
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			if err := wsConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
				b.Fatalf("write: %v", err)
			}
			_, got, err := wsConn.ReadMessage()
			if err != nil {
				b.Fatalf("read: %v", err)
			}
			if !verifyPayload(got, payload) {
				b.Fatalf("WS integrity mismatch")
			}
		}
	})

	b.Run("HTTP", func(b *testing.B) {
		// Brief pause to let TCP/WS sub-benchmarks release ephemeral ports
		time.Sleep(200 * time.Millisecond)
		url := fmt.Sprintf("http://%s/echo", httpAddr)
		client := &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 128,
				IdleConnTimeout:    120 * time.Second,
			},
		}

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			resp, err := retryGet(client, url, 60*time.Second)
			if err != nil {
				b.Fatalf("GET: %v", err)
			}
			resp.Body.Close()
		}
	})
}
