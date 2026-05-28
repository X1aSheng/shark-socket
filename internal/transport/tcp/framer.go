package tcp

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type Framer interface {
	ReadFrame(io.Reader) ([]byte, error)
	WriteFrame(io.Writer, []byte) error
}

type LengthPrefixFramer struct {
	MaxFrameBytes int
}

func (f LengthPrefixFramer) ReadFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint32(header[:]))
	if f.MaxFrameBytes > 0 && n > f.MaxFrameBytes {
		return nil, fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, n, f.MaxFrameBytes)
	}
	payload := make([]byte, n)
	_, err := io.ReadFull(r, payload)
	return payload, err
}

func (f LengthPrefixFramer) WriteFrame(w io.Writer, payload []byte) error {
	if f.MaxFrameBytes > 0 && len(payload) > f.MaxFrameBytes {
		return fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(payload), f.MaxFrameBytes)
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
	var line []byte
	var b [1]byte
	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return nil, err
		}
		line = append(line, b[0])
		if f.MaxFrameBytes > 0 && len(line) > f.MaxFrameBytes {
			return nil, fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(line), f.MaxFrameBytes)
		}
		if b[0] == f.delimiter() {
			break
		}
	}
	return line[:len(line)-1], nil
}

func (f LineFramer) WriteFrame(w io.Writer, payload []byte) error {
	if f.MaxFrameBytes > 0 && len(payload)+1 > f.MaxFrameBytes {
		return fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(payload)+1, f.MaxFrameBytes)
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
	if f.MaxFrameBytes > 0 && len(payload) > f.MaxFrameBytes {
		return fmt.Errorf("%w: %d > %d", core.ErrFrameTooLarge, len(payload), f.MaxFrameBytes)
	}
	_, err := w.Write(payload)
	return err
}
