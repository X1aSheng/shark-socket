package tcp

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// TestSessionSendSharedDoesNotCopy is the core assertion of the shared-frame
// path: the enqueued payload is the frame's own slice, not a copy.
func TestSessionSendSharedDoesNotCopy(t *testing.T) {
	conn := &countedConn{}
	sess := newSession(1, conn, sessionConfig{
		framer:     LengthPrefixFramer{MaxFrameBytes: 4096},
		writeQueue: 4,
	})

	payload := bytes.Repeat([]byte("b"), 128)
	frame := core.NewSharedFrame(payload, 1)
	if err := sess.SendShared(frame); err != nil {
		t.Fatalf("SendShared: %v", err)
	}

	// writeLoop is not running, so the entry is still in the queue.
	pw := <-sess.writeCh
	if len(pw.payload) == 0 || &pw.payload[0] != &frame.Payload()[0] {
		t.Fatal("queued payload is not the frame's own slice (it was copied)")
	}
	if pw.frame != frame {
		t.Fatal("queued entry does not carry the frame; its reference would leak")
	}

	// Put it back and let the write loop consume it.
	sess.writeCh <- pw
	go sess.writeLoop()
	waitForWriteCount(t, conn, 1, 2*time.Second)
	_ = sess.Close(nil)

	if got := frame.Refs(); got != 0 {
		t.Fatalf("frame refs after write = %d, want 0", got)
	}
	if decoded := decodeFrames(t, conn.writtenBytes()); len(decoded) != 1 || !bytes.Equal(decoded[0], payload) {
		t.Fatalf("written frame mismatch: %d frames", len(decoded))
	}
}

// TestSessionCloseReleasesQueuedSharedFrame covers the leak path that matters
// most: a session closed before its write loop reached the frame. The
// reference has to be dropped anyway, or a broadcast pins the payload forever.
func TestSessionCloseReleasesQueuedSharedFrame(t *testing.T) {
	sess := newSession(1, &countedConn{}, sessionConfig{
		framer:     LengthPrefixFramer{MaxFrameBytes: 4096},
		writeQueue: 4,
	})
	frame := core.NewSharedFrame([]byte("payload"), 2) // 2: another consumer exists
	if err := sess.SendShared(frame); err != nil {
		t.Fatalf("SendShared: %v", err)
	}
	if err := sess.Close(nil); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := frame.Refs(); got != 1 {
		t.Fatalf("frame refs after close = %d, want 1 (only this session released)", got)
	}
}

// TestSessionSendSharedHonoursBackpressure asserts a broadcast cannot bypass
// the queue limit that protects a slow consumer: a full queue rejects the
// shared frame, and the reference stays with the caller.
func TestSessionSendSharedHonoursBackpressure(t *testing.T) {
	sess := newSession(1, &countedConn{}, sessionConfig{
		framer:     LengthPrefixFramer{MaxFrameBytes: 4096},
		writeQueue: 1,
	})
	if err := sess.Send([]byte("first")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	frame := core.NewSharedFrame([]byte("second"), 1)
	if err := sess.SendShared(frame); !errors.Is(err, core.ErrWriteQueueFull) {
		t.Fatalf("SendShared error = %v, want ErrWriteQueueFull", err)
	}
	if got := frame.Refs(); got != 1 {
		t.Fatalf("frame refs = %d, want 1 (caller must release on failure)", got)
	}
}

// TestSessionSendSharedClosedSession is the same contract on a closed session.
func TestSessionSendSharedClosedSession(t *testing.T) {
	sess := newSession(1, &countedConn{}, sessionConfig{
		framer:     LengthPrefixFramer{MaxFrameBytes: 4096},
		writeQueue: 4,
	})
	_ = sess.Close(nil)
	frame := core.NewSharedFrame([]byte("x"), 1)
	if err := sess.SendShared(frame); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("SendShared error = %v, want ErrSessionClosed", err)
	}
	if got := frame.Refs(); got != 1 {
		t.Fatalf("frame refs = %d, want 1", got)
	}
	if err := sess.SendShared(nil); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("SendShared(nil) error = %v, want ErrInvalidArgument", err)
	}
}
