package tcp

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Framer interface {
	ReadFrame(io.Reader) ([]byte, error)
	WriteFrame(io.Writer, []byte) error
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

func (f LengthPrefixFramer) WriteFrame(w io.Writer, payload []byte) error {
	max := f.maxBytes()
	if len(payload) > max {
		return fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(payload), max)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
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

func (f LineFramer) WriteFrame(w io.Writer, payload []byte) error {
	max := f.MaxFrameBytes
	if max <= 0 {
		max = defaultMaxFrameBytes
	}
	if len(payload)+1 > max {
		return fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(payload)+1, max)
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err := w.Write([]byte{f.delimiter()})
	return err
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
