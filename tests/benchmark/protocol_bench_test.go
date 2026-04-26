package benchmark_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/infra/bufferpool"
	"github.com/X1aSheng/shark-socket/internal/protocol/coap"
	tcpproto "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
	"github.com/X1aSheng/shark-socket/internal/protocol/udp"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
	"github.com/X1aSheng/shark-socket/internal/utils"
)

// ---------------------------------------------------------------------------
// Session Manager Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkSessionManager_NextID(b *testing.B) {
	mgr := session.NewManager()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mgr.NextID()
	}
}

func BenchmarkSessionManager_NextID_Parallel(b *testing.B) {
	mgr := session.NewManager()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mgr.NextID()
		}
	})
}

// ---------------------------------------------------------------------------
// ShardedMap Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkShardedMap_Set(b *testing.B) {
	sm := utils.NewShardedMap[string, int](16)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.Set(fmt.Sprintf("key-%d", i%1000), i)
	}
}

func BenchmarkShardedMap_Get(b *testing.B) {
	sm := utils.NewShardedMap[string, int](16)
	for i := 0; i < 1000; i++ {
		sm.Set(fmt.Sprintf("key-%d", i), i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.Get(fmt.Sprintf("key-%d", i%1000))
	}
}

func BenchmarkShardedMap_SetGet_Parallel(b *testing.B) {
	sm := utils.NewShardedMap[int, int](16)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sm.Set(i%1000, i)
			sm.Get(i % 1000)
			i++
		}
	})
}

// ---------------------------------------------------------------------------
// BufferPool Benchmarks (cross-level comparison)
// ---------------------------------------------------------------------------

func BenchmarkBufferPool_AllLevels(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"Micro_64", 64},
		{"Tiny_1024", 1024},
		{"Small_4096", 4096},
		{"Medium_16384", 16384},
		{"Large_131072", 131072},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			bp := bufferpool.New()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf := bp.Get(sz.size)
				bp.Put(buf)
			}
		})
	}
}

func BenchmarkBufferPool_Parallel(b *testing.B) {
	bp := bufferpool.New()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := bp.Get(64)
			bp.Put(buf)
		}
	})
}

// ---------------------------------------------------------------------------
// TCP Echo Benchmark
// ---------------------------------------------------------------------------

func BenchmarkTCPEcho(b *testing.B) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := tcpproto.NewServer(handler,
		tcpproto.WithAddr("127.0.0.1", 0),
		tcpproto.WithWorkerPool(4, 16, 128),
		tcpproto.WithFramer(tcpproto.NewLengthPrefixFramer(4096)),
	)
	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer stopServer(srv)

	addr := srv.Addr().String()
	waitTCPReady(b, addr)

	conn, err := retryDial("tcp", addr, 10*time.Second)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(60 * time.Second))

	framer := tcpproto.NewLengthPrefixFramer(4096)
	payload := []byte("bench-tcp-echo")

	// Warm up
	framer.WriteFrame(conn, payload)
	framer.ReadFrame(conn)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := framer.WriteFrame(conn, payload); err != nil {
			b.Fatalf("WriteFrame: %v", err)
		}
		if _, err := framer.ReadFrame(conn); err != nil {
			b.Fatalf("ReadFrame: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// UDP Echo Benchmark
// ---------------------------------------------------------------------------

func BenchmarkUDPEcho(b *testing.B) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := udp.NewServer(handler, udp.WithAddr("127.0.0.1", 0))
	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer stopServer(srv)

	srvAddr := srv.Addr().String()
	waitUDPReady(b, srvAddr)

	clientConn, err := net.DialUDP("udp", nil, srv.Addr().(*net.UDPAddr))
	if err != nil {
		b.Fatalf("DialUDP: %v", err)
	}
	defer clientConn.Close()
	clientConn.SetDeadline(time.Now().Add(10 * time.Second))

	payload := []byte("bench-udp")
	buf := make([]byte, 1500)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clientConn.Write(payload)
		n, err := clientConn.Read(buf)
		if err != nil {
			b.Fatalf("Read: %v", err)
		}
		_ = n
	}
}

// ---------------------------------------------------------------------------
// CoAP Message Serialization Benchmark
// ---------------------------------------------------------------------------

func BenchmarkCoAP_ParseMessage(b *testing.B) {
	msg := &coap.CoAPMessage{
		Version:   1,
		Type:      coap.CON,
		TokenLen:  2,
		Code:      coap.CodeGet,
		MessageID: 0x0001,
		Token:     []byte{0xAB, 0xCD},
		Payload:   []byte("benchmark payload data"),
	}
	data, err := msg.Serialize()
	if err != nil {
		b.Fatalf("Serialize: %v", err)
	}
	if _, err := coap.ParseMessage(data); err != nil {
		b.Fatalf("ParseMessage validation: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := coap.ParseMessage(data)
		if err != nil {
			b.Fatalf("ParseMessage: %v", err)
		}
	}
}

func BenchmarkCoAP_Serialize(b *testing.B) {
	msg := &coap.CoAPMessage{
		Version:   1,
		Type:      coap.CON,
		Code:      coap.CodePost,
		MessageID: 0x0042,
		Payload:   []byte("benchmark payload data"),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := msg.Serialize()
		if err != nil {
			b.Fatalf("Serialize: %v", err)
		}
	}
}
