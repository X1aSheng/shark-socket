package benchmark

import (
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// The benchmarks in this file exist because the two shared structures on the
// per-message and per-connection paths - the plugin chain view and the session
// manager - are read by every goroutine in the process. A single-goroutine
// benchmark cannot see their real cost: an uncontended RWMutex.RLock is a
// couple of nanoseconds, while a contended one is a cache-line transfer per
// call. RunParallel is what turns that difference into a number.

// BenchmarkPluginChain_5Plugins_Parallel drives the chain from every P at once.
// Before ADR-015 every OnMessage took an RLock on one shared RWMutex, so all
// of them fought over the same cache line.
func BenchmarkPluginChain_5Plugins_Parallel(b *testing.B) {
	chain := runtime.NewPluginChain(
		benchPlugin{priority: 1},
		benchPlugin{priority: 2},
		benchPlugin{priority: 3},
		benchPlugin{priority: 4},
		benchPlugin{priority: 5},
	)
	payload := []byte("payload")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		sess := newBenchSession(1, core.ProtocolCustom)
		for pb.Next() {
			if _, err := chain.OnMessage(sess, payload); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkPluginChain_ReconfigureDuringMessage measures what a concurrent
// reconfiguration costs the message path. Reconfiguration is rare, but one that
// stalls every message for the duration of the rebuild is still visible as a
// latency spike.
//
// It republishes the view through SetLogger rather than Append on purpose:
// there is no API to shrink a chain, so an Append loop would grow the chain
// without bound and the benchmark would end up measuring the plugin count that
// happened to accumulate in the time the run took, not the reconfiguration
// cost. SetLogger goes through the same publish path with a fixed chain size.
func BenchmarkPluginChain_ReconfigureDuringMessage(b *testing.B) {
	chain := runtime.NewPluginChain(
		benchPlugin{priority: 1},
		benchPlugin{priority: 2},
	)
	payload := []byte("payload")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			chain.SetLogger(core.NopLogger())
		}
	}()
	b.Cleanup(func() {
		close(stop)
		<-done
	})

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		sess := newBenchSession(1, core.ProtocolCustom)
		for pb.Next() {
			if _, err := chain.OnMessage(sess, payload); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkSessionManager_RegisterGetUnregister_Parallel is the per-connection
// path: every accepted session registers, is looked up, and unregisters. Under
// a single global write lock these serialise across all transports.
func BenchmarkSessionManager_RegisterGetUnregister_Parallel(b *testing.B) {
	manager := runtime.NewSessionManager(runtime.WithMaxSessions(1 << 24))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := manager.NextID()
			sess := newBenchSession(id, core.ProtocolCustom)
			if err := manager.Register(sess); err != nil {
				b.Fatal(err)
			}
			if _, ok := manager.Get(id); !ok {
				b.Fatalf("session %d not found", id)
			}
			manager.Unregister(id)
		}
	})
}
