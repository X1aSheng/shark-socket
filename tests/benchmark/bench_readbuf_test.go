package benchmark

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
)

// countingReader counts underlying Read calls over a fixed byte slice.
//
// On a real connection each of these calls is a syscall, which is what the
// read buffer is meant to reduce. Counting them here keeps the measurement
// deterministic instead of depending on loopback scheduling.
type countingReader struct {
	data  []byte
	off   int
	reads atomic.Int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads.Add(1)
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

// BenchmarkTCPFramerReadCost compares the per-frame read cost with and without
// a read buffer, for the frame sizes the gateway actually sees.
//
// reads/frame is the metric that matters: without buffering it is 2 for a
// length-prefixed frame (header + payload) regardless of size. allocs/frame
// drops from 2 to 1 because the buffered path peeks the header instead of
// passing a stack array to io.ReadFull.
//
// Do not read ns/op here as the buffer's value. The countingReader's Read is a
// memcpy, not a syscall, so this benchmark deliberately understates what the
// buffer saves; the end-to-end comparison is BenchmarkTCPEchoReadBuffer.
func BenchmarkTCPFramerReadCost(b *testing.B) {
	const framesPerIter = 64
	framer := tcp.LengthPrefixFramer{MaxFrameBytes: 64 * 1024}

	for _, size := range []int{64, 256, 1024} {
		frame := frameBytes(repeatBytes(size))
		stream := make([]byte, 0, len(frame)*framesPerIter)
		for i := 0; i < framesPerIter; i++ {
			stream = append(stream, frame...)
		}

		for _, bufSize := range []int{0, 1024, 4096} {
			name := fmt.Sprintf("%s/buf%d", byteSizeName(size), bufSize)
			b.Run(name, func(b *testing.B) {
				src := &countingReader{data: stream}
				var reader io.Reader = src
				var br *bufio.Reader
				if bufSize > 0 {
					br = bufio.NewReaderSize(src, bufSize)
					reader = br
				}

				b.ReportAllocs()
				b.SetBytes(int64(size * framesPerIter))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					src.off = 0
					src.reads.Store(0)
					if br != nil {
						br.Reset(src)
					}
					for f := 0; f < framesPerIter; f++ {
						if _, err := framer.ReadFrame(reader); err != nil {
							b.Fatalf("ReadFrame: %v", err)
						}
					}
				}
				b.StopTimer()
				// Report the last iteration's count, not the sum over b.N: the
				// counter is reset per iteration, so a sum would divide by b.N
				// twice.
				b.ReportMetric(float64(src.reads.Load())/float64(framesPerIter), "reads/frame")
			})
		}
	}
}

// BenchmarkTCPEchoReadBuffer is the end-to-end counterpart: a loopback echo
// round trip with the read buffer disabled, at the 1 KiB default, and at 4 KiB.
//
// It is a development-machine figure. Loopback syscall cost differs from Linux
// cloud hosts, so treat the ratio as the finding and re-measure on the cloud
// baseline before quoting absolute numbers.
func BenchmarkTCPEchoReadBuffer(b *testing.B) {
	skipIfShort(b)
	const payloadSize = 256

	for _, bufSize := range []int{0, 1024, 4096} {
		b.Run(fmt.Sprintf("buf%d", bufSize), func(b *testing.B) {
			h := newEchoHarness(b, func() core.Server {
				return tcp.NewServer(
					tcp.WithAddr("127.0.0.1:0"),
					tcp.WithHandler(echoHandler),
					tcp.WithReadBufferBytes(bufSize),
				)
			})
			client := tcp.NewClient(h.Addr, tcp.WithClientLinger(0))
			if err := client.Connect(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = client.Close() })

			payload := repeatBytes(payloadSize)
			b.SetBytes(payloadSize)
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
				if len(got) != payloadSize {
					b.Fatalf("echo len = %d, want %d", len(got), payloadSize)
				}
			}
		})
	}
}

// BenchmarkTCPEchoWriteBuffer is the write-side A/B: a pipelined echo (16 frames
// out, 16 back) with and without the per-connection write buffer.
//
// Pipelining is what makes the difference measurable. A ping-pong round trip is
// dominated by scheduling, so the two syscalls a frame costs on the write path
// are invisible; with a burst in flight, batching turns 2*16 writes into 1.
//
// The measured Linux figures live in ADR-013 and TRANSPORT.md sec.2.7: 45-49 MB/s
// without the buffer, 60-61 MB/s with it (1.24x-1.28x on two cloud hosts), with
// server write syscalls dropping from one per frame to 0.41-0.55 per frame.
func BenchmarkTCPEchoWriteBuffer(b *testing.B) {
	skipIfShort(b)
	const (
		payloadSize = 256
		pipeline    = 16
	)

	for _, bufSize := range []int{0, 4096} {
		b.Run(fmt.Sprintf("buf%d", bufSize), func(b *testing.B) {
			h := newEchoHarness(b, func() core.Server {
				return tcp.NewServer(
					tcp.WithAddr("127.0.0.1:0"),
					tcp.WithHandler(echoHandler),
					tcp.WithWriteBufferBytes(bufSize),
				)
			})
			client := tcp.NewClient(h.Addr, tcp.WithClientLinger(0))
			if err := client.Connect(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = client.Close() })

			payload := repeatBytes(payloadSize)
			b.SetBytes(payloadSize * pipeline)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for p := 0; p < pipeline; p++ {
					if err := client.Send(payload); err != nil {
						b.Fatal(err)
					}
				}
				for p := 0; p < pipeline; p++ {
					got, err := client.Receive()
					if err != nil {
						b.Fatal(err)
					}
					if len(got) != payloadSize {
						b.Fatalf("echo len = %d, want %d", len(got), payloadSize)
					}
				}
			}
		})
	}
}
