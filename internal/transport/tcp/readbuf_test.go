package tcp

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// byteStream is an io.Reader over a fixed slice with a resettable offset.
// AllocsPerRun needs a reader that can be reused without allocating, otherwise
// the harness would be charged for allocations that belong to the test.
type byteStream struct {
	buf []byte
	off int
}

func (s *byteStream) Read(p []byte) (int, error) {
	if s.off >= len(s.buf) {
		return 0, io.EOF
	}
	n := copy(p, s.buf[s.off:])
	s.off += n
	return n, nil
}

func lengthPrefixed(payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(out[:4], uint32(len(payload)))
	copy(out[4:], payload)
	return out
}

func TestFramerAcceptsBufferedRead(t *testing.T) {
	cases := []struct {
		name   string
		framer Framer
		want   bool
	}{
		{"length_prefix", LengthPrefixFramer{}, true},
		{"length_prefix_ptr", &LengthPrefixFramer{}, true},
		{"line", LineFramer{}, true},
		{"fixed", FixedSizeFramer{Size: 8}, true},
		// RawFramer's output depends on how much one Read returns, so a buffer
		// would silently shrink its frames. Both forms must be excluded.
		{"raw", RawFramer{}, false},
		{"raw_ptr", &RawFramer{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := framerAcceptsBufferedRead(tc.framer); got != tc.want {
				t.Fatalf("framerAcceptsBufferedRead(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestLengthPrefixBufferedOneAllocPerFrame pins the allocation count of the
// buffered read path. Before the buffered path existed this was 2 per frame:
// the payload plus the 4-byte header escaping into io.ReadFull. A regression
// here means the header is escaping again (or a copy was added).
func TestLengthPrefixBufferedOneAllocPerFrame(t *testing.T) {
	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	src := &byteStream{buf: lengthPrefixed(bytes.Repeat([]byte("x"), 64))}
	br := bufio.NewReaderSize(src, 128)

	var sink []byte
	allocs := testing.AllocsPerRun(200, func() {
		src.off = 0
		br.Reset(src)
		p, err := framer.ReadFrame(br)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		sink = p
	})
	if len(sink) != 64 {
		t.Fatalf("payload len = %d, want 64", len(sink))
	}
	if allocs != 1 {
		t.Fatalf("buffered ReadFrame allocs = %.2f, want 1", allocs)
	}
}

func TestLengthPrefixedBufferedErrorIdentity(t *testing.T) {
	framer := LengthPrefixFramer{MaxFrameBytes: 1024}

	t.Run("clean_eof_on_boundary", func(t *testing.T) {
		br := bufio.NewReaderSize(&byteStream{}, 64)
		if _, err := framer.ReadFrame(br); !errors.Is(err, io.EOF) {
			t.Fatalf("err = %v, want io.EOF", err)
		}
	})

	t.Run("eof_mid_header", func(t *testing.T) {
		br := bufio.NewReaderSize(&byteStream{buf: []byte{0, 0}}, 64)
		if _, err := framer.ReadFrame(br); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
		}
	})

	t.Run("oversized_frame", func(t *testing.T) {
		br := bufio.NewReaderSize(&byteStream{buf: lengthPrefixed(make([]byte, 2048))}, 4096)
		if _, err := framer.ReadFrame(br); !errors.Is(err, core.ErrFrameTooLarge) {
			t.Fatalf("err = %v, want ErrFrameTooLarge", err)
		}
	})
}

// TestLineFramerBufferedLongLine covers the ErrBufferFull branch: a line longer
// than the read buffer has to be accumulated across buffer refills.
func TestLineFramerBufferedLongLine(t *testing.T) {
	framer := LineFramer{MaxFrameBytes: 1024}
	line := bytes.Repeat([]byte("x"), 50)
	src := &byteStream{buf: append(append([]byte{}, line...), '\n', 'o', 'k', '\n')}
	br := bufio.NewReaderSize(src, 16) // 16 is bufio's minimum, far below the line

	got, err := framer.ReadFrame(br)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got, line) {
		t.Fatalf("line = %q, want %d x bytes", got, len(line))
	}
	got, err = framer.ReadFrame(br)
	if err != nil {
		t.Fatalf("second ReadFrame: %v", err)
	}
	if string(got) != "ok" {
		t.Fatalf("second line = %q, want ok", got)
	}
}

// TestLineFramerBufferedPayloadDoesNotAliasBuffer is the reason the fast path
// copies. ReadSlice's result points into the reader's buffer; returning it
// directly would let the next read overwrite a payload the caller still holds.
func TestLineFramerBufferedPayloadDoesNotAliasBuffer(t *testing.T) {
	framer := LineFramer{MaxFrameBytes: 1024}
	src := &byteStream{buf: []byte("0123456789abc\nXXXX\n")}
	br := bufio.NewReaderSize(src, 16)

	first, err := framer.ReadFrame(br)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	want := string(first)
	if want != "0123456789abc" {
		t.Fatalf("first line = %q", want)
	}
	// The buffer is empty after the first line, so the next read refills it
	// from offset 0 -- exactly where an aliased payload would live.
	if _, err := framer.ReadFrame(br); err != nil {
		t.Fatalf("second ReadFrame: %v", err)
	}
	if string(first) != want {
		t.Fatalf("first payload mutated to %q, want %q", string(first), want)
	}
}

// TestLineFramerBufferedTooLong asserts the size cap still applies when the
// delimiter never arrives.
func TestLineFramerBufferedTooLong(t *testing.T) {
	framer := LineFramer{MaxFrameBytes: 32}
	src := &byteStream{buf: bytes.Repeat([]byte("x"), 100)}
	br := bufio.NewReaderSize(src, 16)
	if _, err := framer.ReadFrame(br); !errors.Is(err, core.ErrFrameTooLarge) {
		t.Fatalf("err = %v, want ErrFrameTooLarge", err)
	}
}

// countedConn is a net.Conn over a fixed byte slice that counts Read calls.
// It exists to prove the syscall claim at the session level: the number of
// underlying Reads is what becomes syscalls on a real socket.
type countedConn struct {
	mu      sync.Mutex
	data    []byte
	off     int
	reads   int
	writes  int
	written []byte
	closed  bool
}

func (c *countedConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	if c.off >= len(c.data) {
		return 0, io.EOF
	}
	n := copy(p, c.data[c.off:])
	c.off += n
	return n, nil
}

func (c *countedConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// One Append per Write: the count is what the write buffer is meant to
	// reduce, and the bytes are what prove nothing was dropped or reordered.
	c.writes++
	c.written = append(c.written, p...)
	return len(p), nil
}
func (c *countedConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}
func (c *countedConn) LocalAddr() net.Addr              { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *countedConn) RemoteAddr() net.Addr             { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 2)} }
func (c *countedConn) SetDeadline(time.Time) error      { return nil }
func (c *countedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *countedConn) SetWriteDeadline(time.Time) error { return nil }

func (c *countedConn) readCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *countedConn) writeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}

func (c *countedConn) writtenBytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.written...)
}

// TestSessionReadBufferBatchesFrames asserts the end-to-end shape of the
// change: three frames arrive in one buffer fill, so the session performs one
// data Read (plus one Read that reports EOF) instead of two Reads per frame.
func TestSessionReadBufferBatchesFrames(t *testing.T) {
	payloads := [][]byte{
		bytes.Repeat([]byte("a"), 32),
		bytes.Repeat([]byte("b"), 64),
		bytes.Repeat([]byte("c"), 128),
	}
	var stream []byte
	for _, p := range payloads {
		stream = append(stream, lengthPrefixed(p)...)
	}

	run := func(readBuffer int) (reads int, got [][]byte) {
		conn := &countedConn{data: stream}
		sess := newSession(1, conn, sessionConfig{
			framer:          LengthPrefixFramer{MaxFrameBytes: 1024},
			writeQueue:      8,
			readBufferBytes: readBuffer,
		})
		var mu sync.Mutex
		done := make(chan struct{})
		go func() {
			defer close(done)
			sess.readLoop(func(p []byte) {
				mu.Lock()
				got = append(got, p)
				mu.Unlock()
			})
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("readLoop did not finish after EOF")
		}
		mu.Lock()
		defer mu.Unlock()
		return conn.readCount(), got
	}

	bufferedReads, bufferedPayloads := run(1024)
	if len(bufferedPayloads) != len(payloads) {
		t.Fatalf("buffered payload count = %d, want %d", len(bufferedPayloads), len(payloads))
	}
	for i, want := range payloads {
		if !bytes.Equal(bufferedPayloads[i], want) {
			t.Fatalf("buffered payload %d mismatch", i)
		}
	}
	// One fill + one EOF read. The exact number is not the point; that it is
	// independent of frame count (3 frames, not 6 reads) is.
	if bufferedReads > 2 {
		t.Fatalf("buffered session performed %d Reads for %d frames, want <= 2", bufferedReads, len(payloads))
	}

	unbufferedReads, unbufferedPayloads := run(0)
	if len(unbufferedPayloads) != len(payloads) {
		t.Fatalf("unbuffered payload count = %d, want %d", len(unbufferedPayloads), len(payloads))
	}
	// 2 reads per frame (header + payload) + 1 EOF read.
	if want := 2*len(payloads) + 1; unbufferedReads != want {
		t.Fatalf("unbuffered session performed %d Reads, want %d", unbufferedReads, want)
	}
	if bufferedReads >= unbufferedReads {
		t.Fatalf("buffered Reads = %d, unbuffered = %d; buffering saved nothing", bufferedReads, unbufferedReads)
	}
}

// TestSessionRawFramerStaysUnbuffered asserts the exclusion at the point where
// it matters: the session's reader, not the framer type table.
func TestSessionRawFramerStaysUnbuffered(t *testing.T) {
	conn := &countedConn{}
	sess := newSession(1, conn, sessionConfig{
		framer:          RawFramer{MaxFrameBytes: 1024},
		writeQueue:      8,
		readBufferBytes: 4096,
	})
	if sess.reader != conn {
		t.Fatalf("RawFramer session reader is buffered; its frames would shrink")
	}

	conn2 := &countedConn{}
	sess2 := newSession(2, conn2, sessionConfig{
		framer:          LengthPrefixFramer{MaxFrameBytes: 1024},
		writeQueue:      8,
		readBufferBytes: 4096,
	})
	if sess2.reader == conn2 {
		t.Fatalf("LengthPrefixFramer session reader is not buffered")
	}
}
