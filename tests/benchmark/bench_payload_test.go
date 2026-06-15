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
var payloadSizes = []int{64, 1024, 16384, 65507}

func BenchmarkTCPEcho_PayloadSize(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return tcp.NewServer(
			tcp.WithAddr("127.0.0.1:0"),
			tcp.WithHandler(echoHandler),
		)
	})
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
			client := tcp.NewClient(h.Addr, tcp.WithClientLinger(0))
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
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return udp.NewServer(
			udp.WithAddr("127.0.0.1:0"),
			udp.WithHandler(echoHandler),
		)
	})
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
			conn, err := net.Dial("udp", h.Addr)
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
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return websocket.NewServer(
			websocket.WithAddr("127.0.0.1:0"),
			websocket.WithPath("/ws"),
			websocket.WithHandler(echoHandler), websocket.WithCheckOrigin(allowAllOrigins),
		)
	})
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
			u := url.URL{Scheme: "ws", Host: h.Addr, Path: "/ws"}
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
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return transporthttp.NewServer(
			transporthttp.WithAddr("127.0.0.1:0"),
			transporthttp.WithHandler(echoHandler),
		)
	})
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
			client := &http.Client{Timeout: 5 * time.Second, Transport: lingerTransport()}
			endpoint := "http://" + h.Addr + "/"

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
	skipIfShort(b)
	h := newEchoHarness(b, func() core.Server {
		return grpcweb.NewServer(
			grpcweb.WithAddr("127.0.0.1:0"),
			grpcweb.WithHandler(echoHandler), grpcweb.WithCheckOrigin(allowAllOrigins),
		)
	})
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
			client := &http.Client{Timeout: 5 * time.Second, Transport: lingerTransport()}
			url := "http://" + h.Addr + "/grpc"

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
				resp, err := client.Post(url, "application/grpc-web", bytes.NewReader(frame))
				if err != nil {
					b.Fatal(err)
				}
				respBody, readErr := io.ReadAll(resp.Body)
				closeErr := resp.Body.Close()
				if readErr != nil {
					b.Fatal(readErr)
				}
				if closeErr != nil {
					b.Fatal(closeErr)
				}
				if len(respBody) < 5 {
					b.Fatal("response too short")
				}
			}
		})
	}
}

func BenchmarkQUICEcho_PayloadSize(b *testing.B) {
	skipIfShort(b)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{mustGenerateBenchCert(b)},
		NextProtos:   []string{"shark-socket-quic"},
	}
	h := newEchoHarness(b, func() core.Server {
		return quic.NewServer(
			quic.WithAddr("127.0.0.1:0"),
			quic.WithTLS(tlsCfg),
			quic.WithHandler(echoHandler),
		)
	})
	for _, size := range payloadSizes {
		b.Run(byteSizeName(size), func(b *testing.B) {
			payload := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				conn, err := quicgo.DialAddr(context.Background(), h.Addr, quic.ClientTLSConfig(true), nil)
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
				if err := resp.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					_ = conn.CloseWithError(0, "")
					b.Fatal(err)
				}
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
