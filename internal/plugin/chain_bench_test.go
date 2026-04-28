package plugin

import (
	"testing"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// benchPlugin is a minimal no-op plugin for benchmarking the Chain overhead.
type benchPlugin struct {
	name     string
	priority int
}

func (p *benchPlugin) Name() string                                              { return p.name }
func (p *benchPlugin) Priority() int                                             { return p.priority }
func (p *benchPlugin) OnAccept(types.RawSession) error                           { return nil }
func (p *benchPlugin) OnMessage(_ types.RawSession, data []byte) ([]byte, error) { return data, nil }
func (p *benchPlugin) OnClose(types.RawSession)                                  {}

// BenchmarkPluginChain_Empty benchmarks the plugin chain with no plugins.
// This measures the base overhead of the chain dispatch loop.
func BenchmarkPluginChain_Empty(b *testing.B) {
	chain := NewChain()
	sess := newMockSession(1, "127.0.0.1")
	data := []byte("benchmark-data")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = chain.OnMessage(sess, data)
	}
}

// BenchmarkPluginChain_5Plugins benchmarks the plugin chain with 5 pass-through
// plugins. The goal is < 200ns per hop (so < 1000ns total for 5 plugins).
func BenchmarkPluginChain_5Plugins(b *testing.B) {
	plugins := []types.Plugin{
		&benchPlugin{name: "p1", priority: 1},
		&benchPlugin{name: "p2", priority: 2},
		&benchPlugin{name: "p3", priority: 3},
		&benchPlugin{name: "p4", priority: 4},
		&benchPlugin{name: "p5", priority: 5},
	}
	chain := NewChain(plugins...)
	sess := newMockSession(1, "127.0.0.1")
	data := []byte("benchmark-data")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = chain.OnMessage(sess, data)
	}
}

// BenchmarkPluginChain_10Plugins benchmarks the chain with 10 plugins to
// measure scaling behavior.
func BenchmarkPluginChain_10Plugins(b *testing.B) {
	plugins := make([]types.Plugin, 10)
	for i := 0; i < 10; i++ {
		plugins[i] = &benchPlugin{
			name:     "p" + string(rune('A'+i)),
			priority: i + 1,
		}
	}
	chain := NewChain(plugins...)
	sess := newMockSession(1, "127.0.0.1")
	data := []byte("benchmark-data")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = chain.OnMessage(sess, data)
	}
}

// BenchmarkPluginChain_OnAccept_5Plugins benchmarks the OnAccept path with 5 plugins.
func BenchmarkPluginChain_OnAccept_5Plugins(b *testing.B) {
	plugins := []types.Plugin{
		&benchPlugin{name: "p1", priority: 1},
		&benchPlugin{name: "p2", priority: 2},
		&benchPlugin{name: "p3", priority: 3},
		&benchPlugin{name: "p4", priority: 4},
		&benchPlugin{name: "p5", priority: 5},
	}
	chain := NewChain(plugins...)
	sess := newMockSession(1, "127.0.0.1")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = chain.OnAccept(sess)
	}
}

// BenchmarkPluginChain_OnClose_5Plugins benchmarks the OnClose path (reverse order)
// with 5 plugins.
func BenchmarkPluginChain_OnClose_5Plugins(b *testing.B) {
	plugins := []types.Plugin{
		&benchPlugin{name: "p1", priority: 1},
		&benchPlugin{name: "p2", priority: 2},
		&benchPlugin{name: "p3", priority: 3},
		&benchPlugin{name: "p4", priority: 4},
		&benchPlugin{name: "p5", priority: 5},
	}
	chain := NewChain(plugins...)
	sess := newMockSession(1, "127.0.0.1")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		chain.OnClose(sess)
	}
}

// BenchmarkPluginChain_NewChain benchmarks chain creation with 5 plugins.
func BenchmarkPluginChain_NewChain(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = NewChain(
			&benchPlugin{name: "p1", priority: 1},
			&benchPlugin{name: "p2", priority: 2},
			&benchPlugin{name: "p3", priority: 3},
			&benchPlugin{name: "p4", priority: 4},
			&benchPlugin{name: "p5", priority: 5},
		)
	}
}

// BenchmarkPluginChain_Parallel benchmarks OnMessage with 5 plugins under
// concurrent access.
func BenchmarkPluginChain_Parallel(b *testing.B) {
	plugins := []types.Plugin{
		&benchPlugin{name: "p1", priority: 1},
		&benchPlugin{name: "p2", priority: 2},
		&benchPlugin{name: "p3", priority: 3},
		&benchPlugin{name: "p4", priority: 4},
		&benchPlugin{name: "p5", priority: 5},
	}
	chain := NewChain(plugins...)
	data := []byte("benchmark-data")

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		sess := newMockSession(1, "127.0.0.1")
		for pb.Next() {
			_, _ = chain.OnMessage(sess, data)
		}
	})
}
