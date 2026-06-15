package benchmark

import (
	"context"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
)

func BenchmarkPluginChain_Blacklist(b *testing.B) {
	skipIfShort(b)
	h := newEchoHarnessWithPlugins(b, func() core.Server {
		return tcp.NewServer(
			tcp.WithAddr("127.0.0.1:0"),
			tcp.WithHandler(echoHandler),
		)
	}, plugin.NewBlacklist("192.168.0.1", "10.0.0.0/8"))
	client := tcp.NewClient(h.Addr)
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
	}, plugin.NewBlacklist("192.168.0.1", "10.0.0.0/8"))
	client := tcp.NewClient(h.Addr)
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
	}, plugin.NewBlacklist("192.168.0.1", "10.0.0.0/8"))
	client := tcp.NewClient(h.Addr)
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
	}, plugin.NewBlacklist("192.168.0.1", "10.0.0.0/8"))
	client := tcp.NewClient(h.Addr)
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
