package tcp

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// safeFramer creates a new LengthPrefixFramer per ReadFrame call to avoid
// shared bufio.Reader state across concurrent sessions. This is used in
// benchmarks where the server's shared framer option would cause data races.
type safeFramer struct {
	maxSize int
}

func (f *safeFramer) ReadFrame(r io.Reader) ([]byte, error) {
	br := bufio.NewReaderSize(r, 4096)
	header := make([]byte, 4)
	if _, err := io.ReadFull(br, header); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint32(header))
	if f.maxSize > 0 && size > f.maxSize {
		return nil, io.ErrUnexpectedEOF
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(br, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (f *safeFramer) WriteFrame(w io.Writer, payload []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))
	_, err := w.Write(append(header, payload...))
	return err
}

// writeFrame writes a length-prefixed frame directly to a connection.
func writeFrame(conn net.Conn, payload []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))
	_, err := conn.Write(append(header, payload...))
	return err
}

// readFrame reads a length-prefixed frame directly from a connection.
func readFrame(conn net.Conn) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := fullRead(conn, header); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint32(header))
	payload := make([]byte, size)
	if _, err := fullRead(conn, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// fullRead reads exactly len(buf) bytes from conn.
func fullRead(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// BenchmarkTCPEcho benchmarks a full round-trip through the TCP server:
// client sends a framed message, server handler echoes it back,
// client reads the response. Single connection, sequential sends.
func BenchmarkTCPEcho(b *testing.B) {
	// Echo handler: send back whatever we receive.
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithWorkerPool(2, 8, 1024),
		WithWriteQueueSize(1024),
		WithFramer(&safeFramer{maxSize: 4096}),
	)

	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	addr := srv.listener.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(60 * time.Second))

	payload := []byte("benchmark-echo-payload")

	// Warm up: send one message to ensure connection is fully established.
	if err := writeFrame(conn, payload); err != nil {
		b.Fatalf("warmup writeFrame: %v", err)
	}
	if _, err := readFrame(conn); err != nil {
		b.Fatalf("warmup readFrame: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := writeFrame(conn, payload); err != nil {
			b.Fatalf("writeFrame: %v", err)
		}
		got, err := readFrame(conn)
		if err != nil {
			b.Fatalf("readFrame: %v", err)
		}
		if len(got) != len(payload) {
			b.Fatalf("echo length mismatch: got %d, want %d", len(got), len(payload))
		}
	}
}

// BenchmarkTCPEcho_LargeMessage benchmarks echo with a larger payload.
func BenchmarkTCPEcho_LargeMessage(b *testing.B) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithWorkerPool(2, 8, 1024),
		WithWriteQueueSize(1024),
		WithFramer(&safeFramer{maxSize: 65536}),
	)

	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	addr := srv.listener.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(60 * time.Second))

	// 8KB payload
	payload := make([]byte, 8192)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	// Warm up
	if err := writeFrame(conn, payload); err != nil {
		b.Fatalf("warmup writeFrame: %v", err)
	}
	if _, err := readFrame(conn); err != nil {
		b.Fatalf("warmup readFrame: %v", err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := writeFrame(conn, payload); err != nil {
			b.Fatalf("writeFrame: %v", err)
		}
		got, err := readFrame(conn)
		if err != nil {
			b.Fatalf("readFrame: %v", err)
		}
		if len(got) != len(payload) {
			b.Fatalf("echo length mismatch: got %d, want %d", len(got), len(payload))
		}
	}
}

// BenchmarkTCPEcho_Parallel benchmarks multiple concurrent connections
// each doing echo round-trips.
func BenchmarkTCPEcho_Parallel(b *testing.B) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithWorkerPool(8, 32, 4096),
		WithFullPolicy(PolicyBlock),
		WithWriteQueueSize(1024),
		WithFramer(&safeFramer{maxSize: 4096}),
	)

	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	addr := srv.listener.Addr().String()

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			b.Fatalf("dial: %v", err)
		}
		defer conn.Close()

		conn.SetDeadline(time.Now().Add(60 * time.Second))

		payload := []byte("parallel-echo")

		for pb.Next() {
			if err := writeFrame(conn, payload); err != nil {
				b.Fatalf("writeFrame: %v", err)
			}
			got, err := readFrame(conn)
			if err != nil {
				b.Fatalf("readFrame: %v", err)
			}
			if len(got) != len(payload) {
				b.Fatalf("echo length mismatch: got %d, want %d", len(got), len(payload))
			}
		}
	})
}

// BenchmarkTCPEcho_SmallMessage benchmarks echo with a very small payload
// to measure pure framing + dispatch overhead.
func BenchmarkTCPEcho_SmallMessage(b *testing.B) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithWorkerPool(2, 8, 1024),
		WithWriteQueueSize(1024),
		WithFramer(&safeFramer{maxSize: 4096}),
	)

	if err := srv.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	addr := srv.listener.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(60 * time.Second))

	payload := []byte("x")

	// Warm up
	if err := writeFrame(conn, payload); err != nil {
		b.Fatalf("warmup writeFrame: %v", err)
	}
	if _, err := readFrame(conn); err != nil {
		b.Fatalf("warmup readFrame: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := writeFrame(conn, payload); err != nil {
			b.Fatalf("writeFrame: %v", err)
		}
		got, err := readFrame(conn)
		if err != nil {
			b.Fatalf("readFrame: %v", err)
		}
		if len(got) != 1 {
			b.Fatalf("echo length mismatch: got %d, want 1", len(got))
		}
	}
}
