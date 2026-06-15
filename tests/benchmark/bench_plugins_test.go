package benchmark

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/store"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
	"github.com/X1aSheng/shark-socket/internal/transport/udp"
	"github.com/X1aSheng/shark-socket/internal/transport/websocket"
	gws "github.com/gorilla/websocket"
)

func BenchmarkPluginChain_Blacklist(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarnessWithPlugins(b, func() core.Server {
		return tcp.NewServer(
			tcp.WithAddr("127.0.0.1:0"),
			tcp.WithHandler(echoHandler),
		)
	}, plugin.NewBlacklist("192.168.0.1", "10.0.0.0/8"))
	client := tcp.NewClient(h.Addr, tcp.WithClientLinger(0))
	if err := client.Connect(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = client.Close() })

	payload := []byte("benchmark-payload")
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := client.Send(payload); err != nil {
			b.Fatal(err)
		}
		if _, err := client.Receive(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPluginChain_RateLimit(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarnessWithPlugins(b, func() core.Server {
		return tcp.NewServer(
			tcp.WithAddr("127.0.0.1:0"),
			tcp.WithHandler(echoHandler),
		)
	}, plugin.NewRateLimit(1000000, time.Second))
	client := tcp.NewClient(h.Addr, tcp.WithClientLinger(0))
	if err := client.Connect(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = client.Close() })

	payload := []byte("benchmark-payload")
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := client.Send(payload); err != nil {
			b.Fatal(err)
		}
		if _, err := client.Receive(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPluginChain_BlacklistRateLimit(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarnessWithPlugins(b, func() core.Server {
		return tcp.NewServer(
			tcp.WithAddr("127.0.0.1:0"),
			tcp.WithHandler(echoHandler),
		)
	}, plugin.NewBlacklist("192.168.0.1"), plugin.NewRateLimit(1000000, time.Second))
	client := tcp.NewClient(h.Addr, tcp.WithClientLinger(0))
	if err := client.Connect(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = client.Close() })

	payload := []byte("benchmark-payload")
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := client.Send(payload); err != nil {
			b.Fatal(err)
		}
		if _, err := client.Receive(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPluginChain_FullChain(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarnessWithPlugins(b, func() core.Server {
		return tcp.NewServer(
			tcp.WithAddr("127.0.0.1:0"),
			tcp.WithHandler(echoHandler),
		)
	}, plugin.NewBlacklist("192.168.0.1"), plugin.NewAutoBan(100), plugin.NewRateLimit(1000000, time.Second), plugin.NewPersistence(store.NewMemory(), "bench"))
	client := tcp.NewClient(h.Addr, tcp.WithClientLinger(0))
	if err := client.Connect(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = client.Close() })

	payload := []byte("benchmark-payload")
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := client.Send(payload); err != nil {
			b.Fatal(err)
		}
		if _, err := client.Receive(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPluginChain_Blacklist_UDP(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarnessWithPlugins(b, func() core.Server {
		return udp.NewServer(
			udp.WithAddr("127.0.0.1:0"),
			udp.WithHandler(echoHandler),
		)
	}, plugin.NewBlacklist("192.168.0.1", "10.0.0.0/8"))
	conn, err := net.Dial("udp", h.Addr)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })

	payload := []byte("benchmark-payload")
	buf := make([]byte, 1024)
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
		if n != len(payload) {
			b.Fatalf("echo len = %d, want %d", n, len(payload))
		}
	}
}

func BenchmarkPluginChain_RateLimit_UDP(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarnessWithPlugins(b, func() core.Server {
		return udp.NewServer(
			udp.WithAddr("127.0.0.1:0"),
			udp.WithHandler(echoHandler),
		)
	}, plugin.NewRateLimit(1000000, time.Second))
	conn, err := net.Dial("udp", h.Addr)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })

	payload := []byte("benchmark-payload")
	buf := make([]byte, 1024)
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
		if n != len(payload) {
			b.Fatalf("echo len = %d, want %d", n, len(payload))
		}
	}
}

func BenchmarkPluginChain_FullChain_UDP(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarnessWithPlugins(b, func() core.Server {
		return udp.NewServer(
			udp.WithAddr("127.0.0.1:0"),
			udp.WithHandler(echoHandler),
		)
	}, plugin.NewBlacklist("192.168.0.1"), plugin.NewAutoBan(100), plugin.NewRateLimit(1000000, time.Second), plugin.NewPersistence(store.NewMemory(), "bench"))
	conn, err := net.Dial("udp", h.Addr)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })

	payload := []byte("benchmark-payload")
	buf := make([]byte, 1024)
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
		if n != len(payload) {
			b.Fatalf("echo len = %d, want %d", n, len(payload))
		}
	}
}

func BenchmarkPluginChain_Blacklist_WS(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarnessWithPlugins(b, func() core.Server {
		return websocket.NewServer(
			websocket.WithAddr("127.0.0.1:0"),
			websocket.WithPath("/ws"),
			websocket.WithHandler(echoHandler), websocket.WithCheckOrigin(allowAllOrigins),
		)
	}, plugin.NewBlacklist("192.168.0.1", "10.0.0.0/8"))
	u := url.URL{Scheme: "ws", Host: h.Addr, Path: "/ws"}
	conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })

	payload := []byte("benchmark-payload")
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
		if len(got) != len(payload) {
			b.Fatalf("echo len = %d, want %d", len(got), len(payload))
		}
	}
}

func BenchmarkPluginChain_RateLimit_WS(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarnessWithPlugins(b, func() core.Server {
		return websocket.NewServer(
			websocket.WithAddr("127.0.0.1:0"),
			websocket.WithPath("/ws"),
			websocket.WithHandler(echoHandler), websocket.WithCheckOrigin(allowAllOrigins),
		)
	}, plugin.NewRateLimit(1000000, time.Second))
	u := url.URL{Scheme: "ws", Host: h.Addr, Path: "/ws"}
	conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })

	payload := []byte("benchmark-payload")
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
		if len(got) != len(payload) {
			b.Fatalf("echo len = %d, want %d", len(got), len(payload))
		}
	}
}

func BenchmarkPluginChain_FullChain_WS(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarnessWithPlugins(b, func() core.Server {
		return websocket.NewServer(
			websocket.WithAddr("127.0.0.1:0"),
			websocket.WithPath("/ws"),
			websocket.WithHandler(echoHandler), websocket.WithCheckOrigin(allowAllOrigins),
		)
	}, plugin.NewBlacklist("192.168.0.1"), plugin.NewAutoBan(100), plugin.NewRateLimit(1000000, time.Second), plugin.NewPersistence(store.NewMemory(), "bench"))
	u := url.URL{Scheme: "ws", Host: h.Addr, Path: "/ws"}
	conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })

	payload := []byte("benchmark-payload")
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
		if len(got) != len(payload) {
			b.Fatalf("echo len = %d, want %d", len(got), len(payload))
		}
	}
}
