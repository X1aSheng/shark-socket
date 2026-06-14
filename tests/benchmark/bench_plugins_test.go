package benchmark

import (
	"context"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/store"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
)

func BenchmarkPluginChain_Blacklist(b *testing.B) {
	benchTCPWithPlugins(b,
		plugin.NewBlacklist("192.168.0.1", "10.0.0.0/8"),
	)
}

func BenchmarkPluginChain_RateLimit(b *testing.B) {
	benchTCPWithPlugins(b,
		plugin.NewRateLimit(1000000, time.Second), // very high rate so it never blocks
	)
}

func BenchmarkPluginChain_BlacklistRateLimit(b *testing.B) {
	benchTCPWithPlugins(b,
		plugin.NewBlacklist("192.168.0.1"),
		plugin.NewRateLimit(1000000, time.Second),
	)
}

func BenchmarkPluginChain_FullChain(b *testing.B) {
	memStore := store.NewMemory()
	benchTCPWithPlugins(b,
		plugin.NewBlacklist("192.168.0.1"),
		plugin.NewAutoBan(100),
		plugin.NewRateLimit(1000000, time.Second),
		plugin.NewPersistence(memStore, "bench"),
	)
}

// benchTCPWithPlugins creates a TCP server with the given plugins, connects a client,
// and measures echo throughput.
func benchTCPWithPlugins(b *testing.B, plugins ...core.Plugin) {
	// Start TCP server with echo handler
	server := tcp.NewServer(
		tcp.WithAddr("127.0.0.1:0"),
		tcp.WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)

	gateway := runtime.NewGateway(
		runtime.WithPlugins(plugins...),
	)
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
