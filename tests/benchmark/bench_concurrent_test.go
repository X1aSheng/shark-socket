package benchmark

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/grpcweb"
	transporthttp "github.com/X1aSheng/shark-socket/internal/transport/http"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
	"github.com/X1aSheng/shark-socket/internal/transport/udp"
	"github.com/X1aSheng/shark-socket/internal/transport/websocket"
	gws "github.com/gorilla/websocket"
)

// concurrentClients defines the connection counts for concurrent benchmarks.
var concurrentClients = []int{1, 10, 100, 500}

// ---------------------------------------------------------------------------
// TCP concurrent — each parallel goroutine gets its own client
// ---------------------------------------------------------------------------

func BenchmarkTCPEcho_Concurrent(b *testing.B) {
	for _, n := range concurrentClients {
		b.Run(connCountName(n), func(b *testing.B) {
			server := tcp.NewServer(
				tcp.WithAddr("127.0.0.1:0"),
				tcp.WithHandler(func(sess core.Session, msg core.Message) error {
					return sess.Send(msg.Payload)
				}),
			)
			gateway := runtime.NewGateway()
			if err := gateway.Register(server); err != nil {
				b.Fatal(err)
			}
			if err := gateway.Start(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { stopGateway(b, gateway) })

			payload := []byte("benchmark-payload")
			b.ReportAllocs()
			b.ResetTimer()

			// Each parallel goroutine creates its own client
			var mu sync.Mutex
			clients := make([]*tcp.Client, n)
			for i := 0; i < n; i++ {
				c := tcp.NewClient(server.Addr().String())
				if err := c.Connect(context.Background()); err != nil {
					b.Fatal(err)
				}
				clients[i] = c
			}
			next := 0

			b.RunParallel(func(pb *testing.PB) {
				mu.Lock()
				idx := next
				next++
				mu.Unlock()
				client := clients[idx%len(clients)]

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
// UDP concurrent — each parallel goroutine gets its own connection
// ---------------------------------------------------------------------------

func BenchmarkUDPEcho_Concurrent(b *testing.B) {
	for _, n := range concurrentClients {
		b.Run(connCountName(n), func(b *testing.B) {
			server := udp.NewServer(
				udp.WithAddr("127.0.0.1:0"),
				udp.WithHandler(func(sess core.Session, msg core.Message) error {
					return sess.Send(msg.Payload)
				}),
			)
			gateway := runtime.NewGateway()
			if err := gateway.Register(server); err != nil {
				b.Fatal(err)
			}
			if err := gateway.Start(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { stopGateway(b, gateway) })

			payload := []byte("benchmark-payload")
			buf := make([]byte, 1024)
			b.ReportAllocs()
			b.ResetTimer()

			var mu sync.Mutex
			conns := make([]net.Conn, n)
			for i := 0; i < n; i++ {
				c, err := net.Dial("udp", server.Addr().String())
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
	for _, n := range concurrentClients {
		b.Run(connCountName(n), func(b *testing.B) {
			server := websocket.NewServer(
				websocket.WithAddr("127.0.0.1:0"),
				websocket.WithPath("/ws"),
				websocket.WithHandler(func(sess core.Session, msg core.Message) error {
					return sess.Send(msg.Payload)
				}),
			)
			gateway := runtime.NewGateway()
			if err := gateway.Register(server); err != nil {
				b.Fatal(err)
			}
			if err := gateway.Start(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { stopGateway(b, gateway) })

			u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}
			payload := []byte("benchmark-payload")
			b.ReportAllocs()
			b.ResetTimer()

			var mu sync.Mutex
			conns := make([]*gws.Conn, n)
			for i := 0; i < n; i++ {
				c, _, err := gws.DefaultDialer.Dial(u.String(), nil)
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
					if err := conn.WriteMessage(gws.BinaryMessage, payload); err != nil {
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

// ---------------------------------------------------------------------------
// HTTP concurrent — stateless per-request, safe for shared client
// ---------------------------------------------------------------------------

func BenchmarkHTTPEcho_Concurrent(b *testing.B) {
	for _, n := range concurrentClients {
		b.Run(connCountName(n), func(b *testing.B) {
			server := transporthttp.NewServer(
				transporthttp.WithAddr("127.0.0.1:0"),
				transporthttp.WithHandler(func(sess core.Session, msg core.Message) error {
					return sess.Send(msg.Payload)
				}),
			)
			gateway := runtime.NewGateway()
			if err := gateway.Register(server); err != nil {
				b.Fatal(err)
			}
			if err := gateway.Start(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { stopGateway(b, gateway) })

			endpoint := "http://" + server.Addr().String() + "/"
			payload := []byte("benchmark-payload")
			b.ReportAllocs()
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				client := &http.Client{}
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
	for _, n := range concurrentClients {
		b.Run(connCountName(n), func(b *testing.B) {
			server := grpcweb.NewServer(
				grpcweb.WithAddr("127.0.0.1:0"),
				grpcweb.WithHandler(func(sess core.Session, msg core.Message) error {
					return sess.Send(msg.Payload)
				}),
			)
			gateway := runtime.NewGateway()
			if err := gateway.Register(server); err != nil {
				b.Fatal(err)
			}
			if err := gateway.Start(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { stopGateway(b, gateway) })

			url := "http://" + server.Addr().String() + "/grpc"
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
				client := &http.Client{}
				for pb.Next() {
					resp, err := client.Post(url, "application/grpc-web", bytes.NewReader(frame))
					if err != nil {
						b.Fatal(err)
					}
					_, _ = io.ReadAll(resp.Body)
					resp.Body.Close()
				}
			})
		})
	}
}

func connCountName(n int) string {
	return fmt.Sprintf("%dconns", n)
}
