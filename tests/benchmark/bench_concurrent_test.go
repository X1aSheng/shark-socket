package benchmark

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	goruntime "runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/transport/grpcweb"
	transporthttp "github.com/X1aSheng/shark-socket/internal/transport/http"
	"github.com/X1aSheng/shark-socket/internal/transport/quic"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
	"github.com/X1aSheng/shark-socket/internal/transport/udp"
	"github.com/X1aSheng/shark-socket/internal/transport/websocket"
	gws "github.com/gorilla/websocket"
	quicgo "github.com/quic-go/quic-go"
)

// concurrentClientsForOS returns platform-appropriate concurrency levels.
// Windows caps at 50 to avoid ephemeral port exhaustion.
// Set BENCH_MAX_CONNS to override (e.g. BENCH_MAX_CONNS=200).
func concurrentClientsForOS() []int {
	if s := os.Getenv("BENCH_MAX_CONNS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return []int{1, n}
		}
	}
	if goruntime.GOOS == "windows" {
		return []int{1, 10, 50}
	}
	return []int{1, 10, 100, 500}
}

// ---------------------------------------------------------------------------
// TCP concurrent — each parallel goroutine gets its own client
// ---------------------------------------------------------------------------

func BenchmarkTCPEcho_Concurrent(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return tcp.NewServer(
			tcp.WithAddr("127.0.0.1:0"),
			tcp.WithHandler(echoHandler),
		)
	})
	for _, n := range concurrentClientsForOS() {
		b.Run(connCountName(n), func(b *testing.B) {
			payload := []byte("benchmark-payload")
			b.ReportAllocs()
			b.ResetTimer()

			// Each parallel goroutine creates its own dedicated TCP client
			// to avoid concurrent write/read corruption on shared connections.
			b.RunParallel(func(pb *testing.PB) {
				client := tcp.NewClient(h.Addr, tcp.WithClientLinger(0))
				if err := client.Connect(context.Background()); err != nil {
					b.Fatal(err)
				}
				defer client.Close()

				for pb.Next() {
					if err := client.Send(payload); err != nil {
						b.Fatal(err)
					}
					got, err := client.Receive()
					if err != nil {
						b.Fatal(err)
					}
					if !bytes.Equal(got, payload) {
						b.Fatalf("echo = %q, want %q", got, payload)
					}
				}
			})
		})
	}
}

// ---------------------------------------------------------------------------

func BenchmarkUDPEcho_Concurrent(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return udp.NewServer(
			udp.WithAddr("127.0.0.1:0"),
			udp.WithHandler(echoHandler),
		)
	})
	for _, n := range concurrentClientsForOS() {
		b.Run(connCountName(n), func(b *testing.B) {
			payload := []byte("benchmark-payload")
			buf := make([]byte, 1024)
			b.ReportAllocs()
			b.ResetTimer()

			var mu sync.Mutex
			conns := make([]net.Conn, n)
			for i := 0; i < n; i++ {
				c, err := net.Dial("udp", h.Addr)
				if err != nil {
					b.Fatal(err)
				}
				conns[i] = c
			}
			next := 0

			b.RunParallel(func(pb *testing.PB) {
				mu.Lock()
				idx := next
				next++
				mu.Unlock()
				conn := conns[idx%len(conns)]

				for pb.Next() {
					if _, err := conn.Write(payload); err != nil {
						b.Fatal(err)
					}
					if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
						b.Fatal(err)
					}
					_, err := conn.Read(buf)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// ---------------------------------------------------------------------------
// WebSocket concurrent — each parallel goroutine uses its own connection
// (gorilla/websocket is not safe for concurrent writes on the same conn)
// ---------------------------------------------------------------------------

func BenchmarkWSEcho_Concurrent(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return websocket.NewServer(
			websocket.WithAddr("127.0.0.1:0"),
			websocket.WithPath("/ws"),
			websocket.WithHandler(echoHandler), websocket.WithCheckOrigin(allowAllOrigins),
		)
	})
	for _, n := range concurrentClientsForOS() {
		b.Run(connCountName(n), func(b *testing.B) {
			u := url.URL{Scheme: "ws", Host: h.Addr, Path: "/ws"}
			payload := []byte("benchmark-payload")
			b.ReportAllocs()
			b.ResetTimer()

			// Each parallel goroutine creates its own WebSocket connection.
			b.RunParallel(func(pb *testing.PB) {
				conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
				if err != nil {
					b.Fatal(err)
				}
				defer conn.Close()

				for pb.Next() {
					if err := conn.WriteMessage(gws.BinaryMessage, payload); err != nil {
						b.Fatal(err)
					}
					if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
						b.Fatal(err)
					}
					_, got, err := conn.ReadMessage()
					if err != nil {
						b.Fatal(err)
					}
					if !bytes.Equal(got, payload) {
						b.Fatalf("echo = %q, want %q", got, payload)
					}
				}
			})
		})
	}
}

func BenchmarkHTTPEcho_Concurrent(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return transporthttp.NewServer(
			transporthttp.WithAddr("127.0.0.1:0"),
			transporthttp.WithHandler(echoHandler),
		)
	})
	for _, n := range concurrentClientsForOS() {
		b.Run(connCountName(n), func(b *testing.B) {
			endpoint := "http://" + h.Addr + "/"
			payload := []byte("benchmark-payload")
			b.ReportAllocs()
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				client := &http.Client{Transport: lingerTransport()}
				for pb.Next() {
					resp, err := client.Post(endpoint, "application/octet-stream", bytes.NewReader(payload))
					if err != nil {
						b.Fatal(err)
					}
					got, readErr := io.ReadAll(resp.Body)
					closeErr := resp.Body.Close()
					if readErr != nil {
						b.Fatal(readErr)
					}
					if closeErr != nil {
						b.Fatal(closeErr)
					}
					if !bytes.Equal(got, payload) {
						b.Fatalf("echo = %q, want %q", got, payload)
					}
				}
			})
		})
	}
}

// ---------------------------------------------------------------------------
// gRPC-Web concurrent — stateless per-request
// ---------------------------------------------------------------------------

func BenchmarkGRPCWebEcho_Concurrent(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return grpcweb.NewServer(
			grpcweb.WithAddr("127.0.0.1:0"),
			grpcweb.WithHandler(echoHandler), grpcweb.WithCheckOrigin(allowAllOrigins),
		)
	})
	for _, n := range concurrentClientsForOS() {
		b.Run(connCountName(n), func(b *testing.B) {
			url := "http://" + h.Addr + "/grpc"
			payload := []byte("benchmark-payload")
			frame := make([]byte, 5+len(payload))
			frame[0] = 0
			frame[1] = byte(len(payload) >> 24)
			frame[2] = byte(len(payload) >> 16)
			frame[3] = byte(len(payload) >> 8)
			frame[4] = byte(len(payload))
			copy(frame[5:], payload)
			b.ReportAllocs()
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				client := &http.Client{Transport: lingerTransport()}
				for pb.Next() {
					resp, err := client.Post(url, "application/grpc-web", bytes.NewReader(frame))
					if err != nil {
						b.Fatal(err)
					}
					got, readErr := io.ReadAll(resp.Body)
					closeErr := resp.Body.Close()
					if readErr != nil {
						b.Fatal(readErr)
					}
					if closeErr != nil {
						b.Fatal(closeErr)
					}
					_ = got
				}
			})
		})
	}
}

// ---------------------------------------------------------------------------
// QUIC concurrent — each parallel goroutine creates its own connection+stream.
// NOTE: QUIC AcceptStream under concurrency is unreliable on most platforms;
// the server's response-stream model does not support high-concurrency
// round-trips well. This benchmark exists for experimental use only.
// ---------------------------------------------------------------------------

func BenchmarkQUICEcho_Concurrent(b *testing.B) {
	b.Skip("QUIC AcceptStream unreliable under concurrency; kept for experimental use")
	for _, n := range concurrentClientsForOS() {
		b.Run(connCountName(n), func(b *testing.B) {
			cfg := &tls.Config{
				Certificates: []tls.Certificate{mustGenerateBenchCert(b)},
				NextProtos:   []string{"shark-socket-quic"},
			}
			h := newEchoHarness(b, func() core.Server {
				return quic.NewServer(
					quic.WithAddr("127.0.0.1:0"),
					quic.WithTLS(cfg),
					quic.WithHandler(echoHandler),
				)
			})

			payload := []byte("benchmark-payload")
			b.ReportAllocs()
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				conn, err := quicgo.DialAddr(context.Background(), h.Addr, quic.ClientTLSConfig(true), nil)
				if err != nil {
					b.Fatal(err)
				}
				defer conn.CloseWithError(0, "")

				for pb.Next() {
					stream, err := conn.OpenStreamSync(context.Background())
					if err != nil {
						b.Fatal(err)
					}
					_, _ = stream.Write(payload)
					if err := stream.Close(); err != nil {
						b.Fatal(err)
					}
					readCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					resp, err := conn.AcceptStream(readCtx)
					cancel()
					if err != nil {
						b.Fatal(err)
					}
					buf := make([]byte, 1024)
					if err := resp.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
						b.Fatal(err)
					}
					n, err := io.ReadFull(resp, buf[:len(payload)])
					if err != nil {
						b.Fatal(err)
					}
					if !bytes.Equal(buf[:n], payload) {
						b.Fatalf("echo = %q, want %q", buf[:n], payload)
					}
				}
			})
		})
	}
}

func connCountName(n int) string {
	return fmt.Sprintf("%dconns", n)
}
