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
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/grpcweb"
	transporthttp "github.com/X1aSheng/shark-socket/internal/transport/http"
	"github.com/X1aSheng/shark-socket/internal/transport/quic"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
	"github.com/X1aSheng/shark-socket/internal/transport/udp"
	"github.com/X1aSheng/shark-socket/internal/transport/websocket"
	gws "github.com/gorilla/websocket"
	quicgo "github.com/quic-go/quic-go"
)

// payloadSizes is shared across all payload-size benchmarks.
var payloadSizes = []int{64, 1024, 16384, 65536}

func BenchmarkTCPEcho_PayloadSize(b *testing.B) {
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
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

			client := tcp.NewClient(server.Addr().String())
			if err := client.Connect(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = client.Close() })

			payload := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := client.Send(payload); err != nil {
					b.Fatal(err)
				}
				got, err := client.Receive()
				if err != nil {
					b.Fatal(err)
				}
				if len(got) != size {
					b.Fatalf("echo len = %d, want %d", len(got), size)
				}
			}
		})
	}
}

func BenchmarkUDPEcho_PayloadSize(b *testing.B) {
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
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

			conn, err := net.Dial("udp", server.Addr().String())
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = conn.Close() })

			payload := make([]byte, size)
			buf := make([]byte, size+1024)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := conn.Write(payload); err != nil {
					b.Fatal(err)
				}
				n, err := conn.Read(buf)
				if err != nil {
					b.Fatal(err)
				}
				if n != size {
					b.Fatalf("echo len = %d, want %d", n, size)
				}
			}
		})
	}
}

func BenchmarkWSEcho_PayloadSize(b *testing.B) {
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
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
			conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = conn.Close() })

			payload := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := conn.WriteMessage(gws.BinaryMessage, payload); err != nil {
					b.Fatal(err)
				}
				_, got, err := conn.ReadMessage()
				if err != nil {
					b.Fatal(err)
				}
				if len(got) != size {
					b.Fatalf("echo len = %d, want %d", len(got), size)
				}
			}
		})
	}
}

func BenchmarkHTTPEcho_PayloadSize(b *testing.B) {
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
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

			client := &http.Client{}
			endpoint := "http://" + server.Addr().String() + "/"
			payload := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
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
				if len(got) != size {
					b.Fatalf("echo len = %d, want %d", len(got), size)
				}
			}
		})
	}
}

func BenchmarkGRPCWebEcho_PayloadSize(b *testing.B) {
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
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
			payload := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				frame := make([]byte, 5+size)
				frame[0] = 0
				frame[1] = byte(size >> 24)
				frame[2] = byte(size >> 16)
				frame[3] = byte(size >> 8)
				frame[4] = byte(size)
				copy(frame[5:], payload)
				resp, err := http.Post(url, "application/grpc-web", bytes.NewReader(frame))
				if err != nil {
					b.Fatal(err)
				}
				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if len(respBody) < 5 {
					b.Fatal("response too short")
				}
			}
		})
	}
}

func BenchmarkQUICEcho_PayloadSize(b *testing.B) {
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
			tlsCfg := &tls.Config{
				Certificates: []tls.Certificate{mustGenerateBenchCert(b)},
				NextProtos:   []string{"shark-socket-quic"},
			}
			server := quic.NewServer(
				quic.WithAddr("127.0.0.1:0"),
				quic.WithTLS(tlsCfg),
				quic.WithHandler(func(sess core.Session, msg core.Message) error {
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

			payload := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				conn, err := quicgo.DialAddr(context.Background(), server.Addr().String(), quic.ClientTLSConfig(true), nil)
				if err != nil {
					b.Fatal(err)
				}
				stream, err := conn.OpenStreamSync(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				_, _ = stream.Write(payload)
				if err := stream.Close(); err != nil {
					_ = conn.CloseWithError(0, "")
					b.Fatal(err)
				}
				readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				resp, err := conn.AcceptStream(readCtx)
				cancel()
				if err != nil {
					_ = conn.CloseWithError(0, "")
					b.Fatal(err)
				}
				buf := make([]byte, size+1024)
				n, err := io.ReadFull(resp, buf[:size])
				if err != nil {
					_ = conn.CloseWithError(0, "")
					b.Fatal(err)
				}
				_ = conn.CloseWithError(0, "")
				if n != size {
					b.Fatalf("echo len = %d, want %d", n, size)
				}
			}
		})
	}
}

func byteSizeName(size int) string {
	switch {
	case size >= 1024*1024:
		return fmt.Sprintf("%dMB", size/(1024*1024))
	case size >= 1024:
		return fmt.Sprintf("%dKB", size/1024)
	default:
		return fmt.Sprintf("%dB", size)
	}
}
