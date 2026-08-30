package plugin

import (
	"fmt"
	"math"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// BenchmarkRateLimitOnMessage_Parallel measures OnMessage under many-way
// parallel contention from distinct peers. The sharded design keeps
// independent peers on independent locks; a single global mutex would
// serialize all of this traffic. Run with -cpu=1,2,4,8 to observe scaling.
func BenchmarkRateLimitOnMessage_Parallel(b *testing.B) {
	p := NewRateLimit(math.MaxInt, time.Second)
	const peers = 32
	sessions := make([]core.Session, peers)
	for i := range sessions {
		sessions[i] = fakeSession{addr: &net.TCPAddr{IP: net.ParseIP(fmt.Sprintf("10.0.%d.%d", i/250, i%250+1)), Port: 1}}
	}
	data := []byte("x")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = p.OnMessage(sessions[i%peers], data)
			i++
		}
	})
}

// BenchmarkRateLimitOnMessage_SinglePeer measures the uncontended per-message
// cost (single peer, same shard): remote-key cache hit + shard lock + sliding
// window bookkeeping.
func BenchmarkRateLimitOnMessage_SinglePeer(b *testing.B) {
	p := NewRateLimit(math.MaxInt, time.Second)
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1}}
	data := []byte("x")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.OnMessage(sess, data)
	}
}
