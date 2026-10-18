package tcp

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Framer interface {
	ReadFrame(io.Reader) ([]byte, error)
	WriteFrame(io.Writer, []byte) error
}

// framerAcceptsBufferedRead reports whether a framer may be handed a
// *bufio.Reader instead of the raw conn.
//
// RawFramer is the one exclusion: it returns whatever a single Read yields and
// caps the result at MaxFrameBytes, so its output depends on how much the
// reader hands back per call. A 1 KiB read buffer would turn 32 KiB raw reads
// into 1 KiB ones, which is a silent semantic change for its users.
// Every other built-in framer defines frame boundaries itself, so buffering
// only changes syscall counts, not results.
func framerAcceptsBufferedRead(f Framer) bool {
	switch f.(type) {
	case RawFramer, *RawFramer:
		return false
	}
	return true
}

type LengthPrefixFramer struct {
	MaxFrameBytes int
}

// defaultMaxFrameBytes bounds allocations when a framer is used with an
// unset MaxFrameBytes (zero value), preventing a malicious length prefix
// (up to 4 GiB) from triggering an oversized allocation.
const defaultMaxFrameBytes = 1024 * 1024

func (f LengthPrefixFramer) maxBytes() int {
	if f.MaxFrameBytes > 0 {
		return f.MaxFrameBytes
	}
	return defaultMaxFrameBytes
}

func (f LengthPrefixFramer) ReadFrame(r io.Reader) ([]byte, error) {
	if br, ok := r.(*bufio.Reader); ok {
		return f.readFrameBuffered(br)
	}
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	raw := binary.BigEndian.Uint32(header[:])
	max := f.maxBytes()
	// Compare in uint32 space before converting to int: on 32-bit platforms
	// int(uint32) turns values >= 2^31 negative, which would bypass the size
	// check and panic in make([]byte, negative) — a one-frame remote crash.
	if raw > uint32(max) {
		return nil, fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, raw, max)
	}
	payload := make([]byte, int(raw))
	_, err := io.ReadFull(r, payload)
	return payload, err
}

// readFrameBuffered is the *bufio.Reader path: the 4-byte header is read with
// Peek, which returns a slice into the reader's own buffer, so the header never
// becomes the second heap allocation it is on the plain path (where header[:]
// escapes into io.ReadFull). One allocation per frame remains -- the payload --
// which is what the zero-copy work in the redesign has to remove.
func (f LengthPrefixFramer) readFrameBuffered(br *bufio.Reader) ([]byte, error) {
	header, err := br.Peek(4)
	if err != nil {
		// Keep the plain path's error identity: a stream that ends exactly on a
		// frame boundary is io.EOF, one that ends mid-header is
		// io.ErrUnexpectedEOF. Peek cannot tell them apart on its own.
		if err == io.EOF && br.Buffered() > 0 {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	raw := binary.BigEndian.Uint32(header)
	max := f.maxBytes()
	if raw > uint32(max) {
		return nil, fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, raw, max)
	}
	if _, err := br.Discard(4); err != nil {
		return nil, err
	}
	payload := make([]byte, int(raw))
	if _, err := io.ReadFull(br, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (f LengthPrefixFramer) WriteFrame(w io.Writer, payload []byte) error {
	max := f.maxBytes()
	if len(payload) > max {
		return fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(payload), max)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	// One syscall instead of two where writev exists; see framedWrite.
	return framedWrite(w, header[:], payload)
}

type LineFramer struct {
	MaxFrameBytes int
	Delimiter     byte
}

func (f LineFramer) delimiter() byte {
	if f.Delimiter == 0 {
		return '\n'
	}
	return f.Delimiter
}

func (f LineFramer) ReadFrame(r io.Reader) ([]byte, error) {
	if br, ok := r.(*bufio.Reader); ok {
		return f.readFrameBuffered(br)
	}
	max := f.MaxFrameBytes
	if max <= 0 {
		max = defaultMaxFrameBytes
	}
	var line []byte
	var b [1]byte
	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return nil, err
		}
		line = append(line, b[0])
		if len(line) > max {
			return nil, fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(line), max)
		}
		if b[0] == f.delimiter() {
			break
		}
	}
	return line[:len(line)-1], nil
}

// readFrameBuffered scans for the delimiter inside the reader's buffer via
// ReadSlice. The plain path above performs one Read per byte, which on an
// unbuffered conn is one syscall per byte; here the scan is memory-only and the
// payload is copied exactly once (ReadSlice's result is only valid until the
// next read, so it cannot be returned as-is).
func (f LineFramer) readFrameBuffered(br *bufio.Reader) ([]byte, error) {
	max := f.MaxFrameBytes
	if max <= 0 {
		max = defaultMaxFrameBytes
	}
	delim := f.delimiter()
	var out []byte
	for {
		frag, err := br.ReadSlice(delim)
		switch err {
		case nil:
			n := len(frag) - 1 // the delimiter is not part of the payload
			if len(out) == 0 {
				if n > max {
					return nil, fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, n, max)
				}
				line := make([]byte, n)
				copy(line, frag[:n])
				return line, nil
			}
			if len(out)+n > max {
				return nil, fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(out)+n, max)
			}
			return append(out, frag[:n]...), nil
		case bufio.ErrBufferFull:
			// No delimiter in the whole buffer: keep the fragment (it is about
			// to be overwritten) and continue. A line longer than the read
			// buffer costs extra copies, which is the price of not sizing the
			// buffer to MaxFrameBytes.
			out = append(out, frag...)
			if len(out) > max {
				return nil, fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(out), max)
			}
		default:
			return nil, err
		}
	}
}

func (f LineFramer) WriteFrame(w io.Writer, payload []byte) error {
	max := f.MaxFrameBytes
	if max <= 0 {
		max = defaultMaxFrameBytes
	}
	if len(payload)+1 > max {
		return fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(payload)+1, max)
	}
	return framedWrite(w, payload, []byte{f.delimiter()})
}

type FixedSizeFramer struct {
	Size int
}

func (f FixedSizeFramer) ReadFrame(r io.Reader) ([]byte, error) {
	if f.Size <= 0 {
		return nil, fmt.Errorf("fixed frame size must be positive")
	}
	payload := make([]byte, f.Size)
	_, err := io.ReadFull(r, payload)
	return payload, err
}

func (f FixedSizeFramer) WriteFrame(w io.Writer, payload []byte) error {
	if f.Size <= 0 {
		return fmt.Errorf("fixed frame size must be positive")
	}
	if len(payload) != f.Size {
		return fmt.Errorf("fixed frame payload size %d != %d", len(payload), f.Size)
	}
	_, err := w.Write(payload)
	return err
}

type RawFramer struct {
	MaxFrameBytes int
}

func (f RawFramer) ReadFrame(r io.Reader) ([]byte, error) {
	max := f.MaxFrameBytes
	if max <= 0 {
		max = 32 * 1024
	}
	payload := make([]byte, max)
	n, err := r.Read(payload)
	if err != nil {
		return nil, err
	}
	return payload[:n], nil
}

func (f RawFramer) WriteFrame(w io.Writer, payload []byte) error {
	// A zero-value framer defaults the write cap to the same 32 KiB used by
	// ReadFrame, so it can never emit a frame larger than it can read back.
	max := f.MaxFrameBytes
	if max <= 0 {
		max = 32 * 1024
	}
	if len(payload) > max {
		return fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(payload), max)
	}
	_, err := w.Write(payload)
	return err
}
