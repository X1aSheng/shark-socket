package tcp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
)

var ErrSessionClosed = errors.New("session closed")

// pendingWrite is one queued frame.
//
// frame is non-nil when the entry came from SendShared: the write loop must
// release it once the bytes are written (or once the session gives up on
// writing them), otherwise the shared payload stays referenced forever.
type pendingWrite struct {
	payload []byte
	frame   *core.SharedFrame
}

type session struct {
	id   uint64
	conn net.Conn
	// reader is what the framer reads from: a buffered reader when
	// ReadBufferBytes is set, otherwise the conn itself. Keeping it as a field
	// (never nil) means readLoop has no branch and tests can swap it.
	reader io.Reader
	// writer is the mirror of reader: the bufio.Writer when WriteBufferBytes is
	// set, otherwise the conn. bw is the same object when it exists, kept
	// because Flush is not part of io.Writer.
	writer              io.Writer
	bw                  *bufio.Writer
	framer              Framer
	createdAt           time.Time
	activeAt            atomic.Int64
	state               atomic.Uint32
	meta                sync.Map
	ctx                 context.Context
	cancel              context.CancelFunc
	writeCh             chan pendingWrite
	writeTimeout        time.Duration
	readTimeout         time.Duration
	writeQueueHighWater float64
	onIdleClose         func() // invoked when a read deadline reclaims the session
	closeOnce           sync.Once
}

// sessionConfig carries the per-session options that come from the server.
// It exists because newSession was accumulating positional parameters: two of
// them (read and write buffer sizes) are per-connection memory knobs that a
// caller is likely to want to set together, and a mistake in argument order
// would not be caught by the compiler.
type sessionConfig struct {
	framer              Framer
	writeQueue          int
	writeTimeout        time.Duration
	writeQueueHighWater float64
	readBufferBytes     int
	writeBufferBytes    int
}

func newSession(id uint64, conn net.Conn, cfg sessionConfig) *session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		id:                  id,
		conn:                conn,
		framer:              cfg.framer,
		createdAt:           time.Now(),
		ctx:                 ctx,
		cancel:              cancel,
		writeCh:             make(chan pendingWrite, cfg.writeQueue),
		writeTimeout:        cfg.writeTimeout,
		writeQueueHighWater: cfg.writeQueueHighWater,
	}
	// The read buffer is a per-connection cost traded for one underlying Read
	// per buffer fill instead of one per frame. A RawFramer is excluded: its
	// contract is "return whatever one Read yields", so a buffer would
	// silently shrink the frames it hands back.
	if cfg.readBufferBytes > 0 && framerAcceptsBufferedRead(cfg.framer) {
		s.reader = bufio.NewReaderSize(conn, cfg.readBufferBytes)
	} else {
		s.reader = conn
	}
	// The write buffer costs just as much as the read buffer, so it is opt-in:
	// it only pays for itself when frames are small and bursts are common.
	if cfg.writeBufferBytes > 0 {
		s.bw = bufio.NewWriterSize(conn, cfg.writeBufferBytes)
		s.writer = s.bw
	} else {
		s.writer = conn
	}
	s.activeAt.Store(time.Now().UnixNano())
	s.state.Store(uint32(core.StateActive))
	return s
}

func (s *session) ID() uint64                   { return s.id }
func (s *session) Protocol() core.Protocol      { return core.ProtocolTCP }
func (s *session) RemoteAddr() net.Addr         { return s.conn.RemoteAddr() }
func (s *session) LocalAddr() net.Addr          { return s.conn.LocalAddr() }
func (s *session) CreatedAt() time.Time         { return s.createdAt }
func (s *session) LastActiveAt() time.Time      { return time.Unix(0, s.activeAt.Load()) }
func (s *session) Context() context.Context     { return s.ctx }
func (s *session) SetMeta(k string, v any)      { s.meta.Store(k, v) }
func (s *session) GetMeta(k string) (any, bool) { return s.meta.Load(k) }
func (s *session) DelMeta(k string)             { s.meta.Delete(k) }

func (s *session) State() core.SessionState {
	return core.SessionState(s.state.Load())
}

func (s *session) touch() {
	s.activeAt.Store(time.Now().UnixNano())
}

func (s *session) Send(payload []byte) error {
	if s.State() != core.StateActive {
		return ErrSessionClosed
	}
	// High-water backpressure: reject before the queue is saturated so a slow
	// consumer sheds load earlier (WriteQueueHighWater, default 0.8 of the
	// queue capacity; 0 disables). The threshold is clamped to at least 1 so a
	// small queue (e.g. WithWriteQueue(1)) still accepts its first write
	// instead of truncating 0.8*cap to 0 and rejecting everything.
	if s.writeQueueHighWater > 0 {
		threshold := int(float64(cap(s.writeCh)) * s.writeQueueHighWater)
		if threshold < 1 {
			threshold = 1
		}
		if len(s.writeCh) >= threshold {
			return core.ErrWriteQueueFull
		}
	}
	copied := append([]byte(nil), payload...)
	return s.enqueue(pendingWrite{payload: copied})
}

// SendShared enqueues a reference-counted frame without copying it, so a
// broadcast to N sessions costs one copy instead of N.
//
// Reference ownership: a successful call transfers one reference to the
// session, which releases it after the write (or on close, if the write never
// happens). A failed call does not, so the caller keeps the reference and is
// responsible for releasing it -- otherwise a frame would be silently pinned
// whenever one recipient out of many rejected it.
func (s *session) SendShared(frame *core.SharedFrame) error {
	if frame == nil {
		return core.ErrInvalidArgument
	}
	if s.State() != core.StateActive {
		// Same sentinel as Send: callers match on tcp.ErrSessionClosed.
		return ErrSessionClosed
	}
	return s.enqueue(pendingWrite{payload: frame.Payload(), frame: frame})
}

// enqueue applies the high-water backpressure and hands the entry to the write
// loop. Shared and non-shared sends share this path so a broadcast cannot
// bypass the queue limits that protect a slow consumer.
func (s *session) enqueue(pw pendingWrite) error {
	// High-water backpressure: reject before the queue is saturated so a slow
	// consumer sheds load earlier (WriteQueueHighWater, default 0.8 of the
	// queue capacity; 0 disables). The threshold is clamped to at least 1 so a
	// small queue (e.g. WithWriteQueue(1)) still accepts its first write
	// instead of truncating 0.8*cap to 0 and rejecting everything.
	if s.writeQueueHighWater > 0 {
		threshold := int(float64(cap(s.writeCh)) * s.writeQueueHighWater)
		if threshold < 1 {
			threshold = 1
		}
		if len(s.writeCh) >= threshold {
			return core.ErrWriteQueueFull
		}
	}
	select {
	case s.writeCh <- pw:
		return nil
	case <-s.ctx.Done():
		return ErrSessionClosed
	default:
		return core.ErrWriteQueueFull
	}
}

func (s *session) Close(context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.state.Store(uint32(core.StateClosed))
		s.cancel()
		// writeCh is intentionally never closed: Send and writeLoop
		// both select on ctx.Done() so no "send on closed channel"
		// panic can occur during a concurrent Close.
		s.releaseQueuedFrames()
		err = s.conn.Close()
	})
	return err
}

func (s *session) readLoop(handler func([]byte)) {
	defer func() { _ = s.Close(context.Background()) }()
	for {
		if s.readTimeout > 0 {
			if err := s.conn.SetReadDeadline(time.Now().Add(s.readTimeout)); err != nil {
				return
			}
		}
		payload, err := s.framer.ReadFrame(s.reader)
		if err != nil {
			// A deadline expiry means the peer went silent (a half-open or
			// zombie connection) rather than disconnecting cleanly; surface
			// the reclaim so the server can count it.
			if shared.IsTimeout(err) && s.onIdleClose != nil {
				s.onIdleClose()
			}
			return
		}
		s.touch()
		handler(payload)
	}
}

func (s *session) writeLoop() {
	defer func() { _ = s.Close(context.Background()) }()
	for {
		select {
		case pw := <-s.writeCh:
			if err := s.writeBatch(pw); err != nil {
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// writeBatch writes the frame that woke writeLoop plus everything already
// queued behind it, then flushes once.
//
// Batching is invisible at low load: when the queue is empty after the first
// frame the flush happens immediately, so latency is unchanged. It only shows
// up under a burst, where the whole burst leaves in one syscall instead of one
// (header + payload) per frame. That is also why it is opt-in: the buffer is
// per-connection heap, and a connection that never bursts pays for it anyway.
//
// The write deadline is set once per batch instead of once per frame. It bounds
// how long an unresponsive peer can hold the write path, which is the same
// guarantee at batch granularity.
func (s *session) writeBatch(first pendingWrite) error {
	if s.writeTimeout > 0 {
		if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
			return err
		}
	}
	if err := s.writeOne(first); err != nil {
		return err
	}
	if s.bw == nil {
		return nil
	}
	// Drain what is already queued before flushing. writeLoop is the only
	// receiver on writeCh, so a non-empty channel here cannot be stolen.
	// A producer faster than the socket is not starved: bufio flushes on its
	// own as soon as the next frame does not fit in the remaining buffer.
	for len(s.writeCh) > 0 {
		pw := <-s.writeCh
		if err := s.writeOne(pw); err != nil {
			return err
		}
	}
	return s.bw.Flush()
}

// writeOne writes one queued entry and, for a shared frame, drops its
// reference: the bytes have reached the socket, so this consumer is done with
// them.
func (s *session) writeOne(pw pendingWrite) error {
	err := s.framer.WriteFrame(s.writer, pw.payload)
	if pw.frame != nil {
		pw.frame.Release()
	}
	return err
}

// releaseQueuedFrames drops the references of entries that were queued but
// never written. Without this a broadcast frame would stay referenced by every
// session that closed (or was closed) before its write loop reached it.
func (s *session) releaseQueuedFrames() {
	for {
		select {
		case pw := <-s.writeCh:
			if pw.frame != nil {
				pw.frame.Release()
			}
		default:
			return
		}
	}
}

var (
	_ core.Session      = (*session)(nil)
	_ core.SharedSender = (*session)(nil)
)
