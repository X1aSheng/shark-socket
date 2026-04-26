package session

import (
	"net"
	"sync/atomic"
	"testing"

	"github.com/yourname/shark-socket/internal/types"
)

// benchSession is a minimal RawSession for benchmarking the Manager.
// It embeds *BaseSession and provides stub Send/SendTyped/Close methods.
type benchSession struct {
	*BaseSession
	closed atomic.Bool
}

func newBenchSession(id uint64) *benchSession {
	return &benchSession{
		BaseSession: NewBase(id, types.TCP,
			&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345},
			&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080},
		),
	}
}

func (s *benchSession) Send(data []byte) error { return nil }

func (s *benchSession) SendTyped(msg []byte) error { return nil }

func (s *benchSession) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		s.DoClose()
	}
	return nil
}

// BenchmarkSessionRegister benchmarks parallel Register/Unregister cycles
// on the Manager to measure sharded map throughput under contention.
func BenchmarkSessionRegister(b *testing.B) {
	m := NewManager()
	defer m.Close()

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := m.NextID()
			sess := newBenchSession(id)
			if err := m.Register(sess); err != nil {
				b.Fatalf("Register: %v", err)
			}
			m.Unregister(id)
		}
	})
}

// BenchmarkManagerGet benchmarks parallel Get lookups on a pre-populated
// Manager to measure read-path throughput under contention.
func BenchmarkManagerGet(b *testing.B) {
	m := NewManager()
	defer m.Close()

	// Pre-register sessions across all shards.
	const numPre = 10000
	ids := make([]uint64, numPre)
	for i := 0; i < numPre; i++ {
		id := m.NextID()
		ids[i] = id
		sess := newBenchSession(id)
		if err := m.Register(sess); err != nil {
			b.Fatalf("pre-register: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := uint64(0)
		for pb.Next() {
			idx := atomic.AddUint64(&i, 1) % uint64(numPre)
			_, ok := m.Get(ids[idx])
			if !ok {
				b.Fatalf("Get(%d) not found", ids[idx])
			}
		}
	})
}

// BenchmarkManagerNextID benchmarks the atomic ID generator.
func BenchmarkManagerNextID(b *testing.B) {
	m := NewManager()
	defer m.Close()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = m.NextID()
	}
}

// BenchmarkManagerNextIDParallel benchmarks the atomic ID generator under
// concurrent access.
func BenchmarkManagerNextIDParallel(b *testing.B) {
	m := NewManager()
	defer m.Close()

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = m.NextID()
		}
	})
}

// BenchmarkManagerCount benchmarks the lock-free Count() operation.
func BenchmarkManagerCount(b *testing.B) {
	m := NewManager()
	defer m.Close()

	// Register some sessions so count is non-trivial.
	for i := 0; i < 1000; i++ {
		sess := newBenchSession(m.NextID())
		m.Register(sess)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = m.Count()
	}
}

// BenchmarkManagerRegisterSequential benchmarks single-threaded Register
// to measure the uncontended overhead.
func BenchmarkManagerRegisterSequential(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		m := NewManager()
		for j := 0; j < 1000; j++ {
			sess := newBenchSession(m.NextID())
			m.Register(sess)
		}
		m.Close()
	}
}
