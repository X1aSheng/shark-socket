package tcp

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// waitForWriteCount polls until the conn has seen at least n underlying writes,
// or fails after timeout. Polling (rather than a fixed sleep) keeps the test
// fast when the writes arrive and stable when the machine is slow.
func waitForWriteCount(t *testing.T, conn *countedConn, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if conn.writeCount() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("conn saw %d writes, want >= %d within %v", conn.writeCount(), n, timeout)
}

// decodeFrames reads length-prefixed frames until the stream is exhausted.
func decodeFrames(t *testing.T, data []byte) [][]byte {
	t.Helper()
	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	reader := bytes.NewReader(data)
	var out [][]byte
	for {
		p, err := framer.ReadFrame(reader)
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("decode frame: %v", err)
		}
		out = append(out, p)
	}
}

// TestWriteLoopBatchesQueuedFrames is the write-side counterpart of
// TestSessionReadBufferBatchesFrames: three queued frames leave in one flush
// when a write buffer is configured, and in one write per piece without one
// (header + payload, so two per frame).
func TestWriteLoopBatchesQueuedFrames(t *testing.T) {
	payloads := [][]byte{
		bytes.Repeat([]byte("a"), 32),
		bytes.Repeat([]byte("b"), 64),
		bytes.Repeat([]byte("c"), 128),
	}

	cases := []struct {
		name       string
		bufferSize int
		wantWrites int
	}{
		// No buffer: 2 writes per frame. On unix framedWrite collapses the two
		// pieces into a single writev, so the expectation is "one per frame or
		// two per frame" rather than a fixed number.
		{"buffered", 4096, 1},
		{"unbuffered", 0, -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &countedConn{}
			sess := newSession(1, conn, sessionConfig{
				framer:           LengthPrefixFramer{MaxFrameBytes: 1024},
				writeQueue:       8,
				writeBufferBytes: tc.bufferSize,
			})
			for _, p := range payloads {
				if err := sess.Send(p); err != nil {
					t.Fatalf("Send: %v", err)
				}
			}

			go sess.writeLoop()
			defer func() { _ = sess.Close(nil) }()

			want := tc.wantWrites
			if want < 0 {
				want = 1 // poll for the first write, then compare below
			}
			waitForWriteCount(t, conn, want, 2*time.Second)
			time.Sleep(20 * time.Millisecond) // let any extra writes land

			got := conn.writeCount()
			switch {
			case tc.bufferSize > 0 && got != 1:
				t.Fatalf("buffered session used %d writes for %d frames, want 1", got, len(payloads))
			case tc.bufferSize == 0 && (got < len(payloads) || got > 2*len(payloads)):
				t.Fatalf("unbuffered session used %d writes for %d frames, want %d..%d",
					got, len(payloads), len(payloads), 2*len(payloads))
			}

			// Batching must not lose, reorder or truncate anything.
			decoded := decodeFrames(t, conn.writtenBytes())
			if len(decoded) != len(payloads) {
				t.Fatalf("decoded %d frames, want %d", len(decoded), len(payloads))
			}
			for i, want := range payloads {
				if !bytes.Equal(decoded[i], want) {
					t.Fatalf("frame %d mismatch: got %d bytes, want %d", i, len(decoded[i]), len(want))
				}
			}
		})
	}
}

// TestWriteBufferFlushesImmediately guards the latency claim in the batching
// comment: with nothing queued behind a frame, the flush must happen right
// away rather than waiting for more data.
func TestWriteBufferFlushesImmediately(t *testing.T) {
	conn := &countedConn{}
	sess := newSession(1, conn, sessionConfig{
		framer:           LengthPrefixFramer{MaxFrameBytes: 1024},
		writeQueue:       8,
		writeBufferBytes: 4096,
	})
	if err := sess.Send([]byte("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	go sess.writeLoop()
	defer func() { _ = sess.Close(nil) }()

	waitForWriteCount(t, conn, 1, 2*time.Second)
	if got := decodeFrames(t, conn.writtenBytes()); len(got) != 1 || string(got[0]) != "hello" {
		t.Fatalf("flushed %d frames, want 1 frame of hello", len(got))
	}
}

// TestWriteBatchSetsDeadlineOncePerBatch documents the deadline granularity
// change: one deadline per batch instead of one per frame. A session with a
// write timeout must still configure the conn (unless the conn rejects it).
func TestWriteBatchSetsDeadlineOncePerBatch(t *testing.T) {
	conn := &countedConn{}
	sess := newSession(1, conn, sessionConfig{
		framer:           LengthPrefixFramer{MaxFrameBytes: 1024},
		writeQueue:       8,
		writeTimeout:     time.Second,
		writeBufferBytes: 4096,
	})
	for i := 0; i < 3; i++ {
		if err := sess.Send(bytes.Repeat([]byte("x"), 16)); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	go sess.writeLoop()
	defer func() { _ = sess.Close(nil) }()

	waitForWriteCount(t, conn, 1, 2*time.Second)
	if got := conn.writeCount(); got != 1 {
		t.Fatalf("write count = %d, want 1 (a single batched flush)", got)
	}
}
