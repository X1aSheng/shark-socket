package tcp

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// batchCountingFramer records, per frame, whether the writer was empty when the
// frame started. An empty buffer means everything written so far has already
// left for the socket, so the count of empty-at-entry frames is the number of
// batches -- i.e. the number of underlying writes, without having to wrap the
// connection (which the server does not expose).
//
// It measures the frame writes only; the reads passing through it are the
// server's, and they are irrelevant here.
type batchCountingFramer struct {
	inner Framer

	frames  atomic.Int64
	batches atomic.Int64
	// sawBuffered is false when the session handed us the raw conn, which is
	// what happens with WriteBufferBytes == 0. Distinguishing the two cases is
	// the point of the control arm below.
	sawBuffered atomic.Bool
}

func (f *batchCountingFramer) WriteFrame(w io.Writer, payload []byte) error {
	f.frames.Add(1)
	if bw, ok := w.(*bufio.Writer); ok {
		f.sawBuffered.Store(true)
		if bw.Buffered() == 0 {
			f.batches.Add(1)
		}
	}
	return f.inner.WriteFrame(w, payload)
}

func (f *batchCountingFramer) ReadFrame(r io.Reader) ([]byte, error) {
	return f.inner.ReadFrame(r)
}

// TestWriteBatchingEngagesUnderRealIO closes the gap the session-level test
// leaves open: there, the frames are queued before writeLoop starts, so
// batching engages by construction. Here a real client sends a burst over a
// real socket and the question is whether the queue actually holds more than
// one frame when the write loop wakes -- if it never does, the feature is dead
// code and ADR-013 says to delete it rather than keep an unused switch.
//
// The echo is deliberate: N payloads are held until the last one arrives, then
// sent back in a tight loop. That is the shape of a broadcast or a
// request-response fan-in, and it is the case the buffer exists for.
func TestWriteBatchingEngagesUnderRealIO(t *testing.T) {
	const (
		payloadSize = 32
		burst       = 64
		bufferBytes = 4096
	)
	// 64 frames of 36 bytes is well under the 4096-byte buffer, so a single
	// flush is physically possible; the bound below allows scheduling to split
	// it a few ways without letting "no batching at all" pass.
	wantAtMostBatches := int64(8)

	cases := []struct {
		name        string
		bufferSize  int
		wantBuf     bool
		maxBatches  int64
		description string
	}{
		{name: "buffered", bufferSize: bufferBytes, wantBuf: true, maxBatches: wantAtMostBatches,
			description: "one flush per burst"},
		{name: "unbuffered", bufferSize: 0, wantBuf: false, maxBatches: burst,
			description: "one write path per frame: the control arm"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			framer := &batchCountingFramer{inner: LengthPrefixFramer{MaxFrameBytes: 1024}}

			var (
				mu     sync.Mutex
				got    [][]byte
				echoed bool
			)
			server := NewServer(
				WithAddr("127.0.0.1:0"),
				WithFramer(framer),
				WithWriteBufferBytes(tc.bufferSize),
				WithWriteQueue(burst*2),
				WithHandler(func(sess core.Session, msg core.Message) error {
					mu.Lock()
					got = append(got, append([]byte(nil), msg.Payload...))
					if len(got) < burst || echoed {
						mu.Unlock()
						return nil
					}
					echoed = true
					batch := got
					mu.Unlock()
					// Sending the whole burst from here is what gives the write
					// loop something queued to drain.
					for _, p := range batch {
						if err := sess.Send(p); err != nil {
							return err
						}
					}
					return nil
				}),
			)
			gateway := runtime.NewGateway()
			if err := gateway.Register(server); err != nil {
				t.Fatal(err)
			}
			if err := gateway.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = gateway.Stop(ctx)
			}()

			conn, err := dialWithLinger(context.Background(), "tcp", server.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			// Send the burst before reading anything: a blocking request-response
			// loop would never let the server queue a second frame.
			payload := bytes.Repeat([]byte("x"), payloadSize)
			w := LengthPrefixFramer{MaxFrameBytes: 1024}
			for i := 0; i < burst; i++ {
				if err := w.WriteFrame(conn, payload); err != nil {
					t.Fatalf("send %d: %v", i, err)
				}
			}

			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			r := LengthPrefixFramer{MaxFrameBytes: 1024}
			for i := 0; i < burst; i++ {
				reply, err := r.ReadFrame(conn)
				if err != nil {
					t.Fatalf("reply %d: %v (batching must not lose or truncate frames)", i, err)
				}
				if !bytes.Equal(reply, payload) {
					t.Fatalf("reply %d: got %d bytes, want %d", i, len(reply), payloadSize)
				}
			}

			if got := framer.frames.Load(); got != burst {
				t.Fatalf("server wrote %d frames, want %d", got, burst)
			}
			if got := framer.sawBuffered.Load(); got != tc.wantBuf {
				t.Fatalf("framer saw a bufio.Writer = %v, want %v (WriteBufferBytes=%d)",
					got, tc.wantBuf, tc.bufferSize)
			}
			batches := framer.batches.Load()
			if batches > tc.maxBatches {
				t.Fatalf("%s: %d batches for %d frames (limit %d)",
					tc.description, batches, burst, tc.maxBatches)
			}
			// Logged so a -v run records the ratio the Linux probe script is
			// meant to reproduce with strace.
			if tc.wantBuf {
				t.Logf("%s: %d frames in %d batches (%.1f frames per write)",
					tc.name, burst, batches, float64(burst)/float64(batches))
			} else {
				t.Logf("%s: %d frames, no buffer -- every frame is its own write path",
					tc.name, burst)
			}
		})
	}
}

// batchCountingFramer must stay a Framer: the server hands it to every session.
var _ Framer = (*batchCountingFramer)(nil)
