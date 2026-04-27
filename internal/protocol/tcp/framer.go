package tcp

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// Framer reads and writes framed messages from a stream.
type Framer interface {
	ReadFrame(r io.Reader) ([]byte, error)
	WriteFrame(w io.Writer, payload []byte) error
}

// LengthPrefixFramer uses a 4-byte big-endian length prefix.
type LengthPrefixFramer struct {
	maxSize int
}

// NewLengthPrefixFramer creates a new LengthPrefixFramer.
func NewLengthPrefixFramer(maxSize int) *LengthPrefixFramer {
	return &LengthPrefixFramer{
		maxSize: maxSize,
	}
}

func (f *LengthPrefixFramer) ReadFrame(r io.Reader) (payload []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("invalid frame: %v", r)
		}
	}()

	br := bufio.NewReaderSize(r, 4096)
	header := make([]byte, 4)
	if _, err := io.ReadFull(br, header); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint32(header))
	if f.maxSize > 0 && size > f.maxSize {
		return nil, fmt.Errorf("frame too large: %d > %d", size, f.maxSize)
	}
	if size == 0 {
		return nil, nil
	}
	payload = make([]byte, size)
	if _, err := io.ReadFull(br, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (f *LengthPrefixFramer) WriteFrame(w io.Writer, payload []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))
	_, err := w.Write(append(header, payload...))
	return err
}

// LineFramer uses newline-delimited frames.
type LineFramer struct {
	maxSize int
}

// NewLineFramer creates a new LineFramer.
func NewLineFramer(maxSize int) *LineFramer {
	return &LineFramer{maxSize: maxSize}
}

func (f *LineFramer) ReadFrame(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	data := line[:len(line)-1]
	if f.maxSize > 0 && len(data) > f.maxSize {
		return nil, fmt.Errorf("line frame too large: %d > %d", len(data), f.maxSize)
	}
	return data, nil
}

func (f *LineFramer) WriteFrame(w io.Writer, payload []byte) error {
	_, err := w.Write(append(payload, '\n'))
	return err
}

// FixedSizeFramer reads fixed-size frames.
type FixedSizeFramer struct {
	size int
}

// NewFixedSizeFramer creates a new FixedSizeFramer.
func NewFixedSizeFramer(size int) *FixedSizeFramer {
	return &FixedSizeFramer{size: size}
}

func (f *FixedSizeFramer) ReadFrame(r io.Reader) ([]byte, error) {
	buf := make([]byte, f.size)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func (f *FixedSizeFramer) WriteFrame(w io.Writer, payload []byte) error {
	_, err := w.Write(payload)
	return err
}

// RawFramer passes data through without framing.
type RawFramer struct {
	bufSize int
}

// NewRawFramer creates a new RawFramer.
func NewRawFramer(bufSize int) *RawFramer {
	if bufSize <= 0 {
		bufSize = 4096
	}
	return &RawFramer{bufSize: bufSize}
}

func (f *RawFramer) ReadFrame(r io.Reader) ([]byte, error) {
	buf := make([]byte, f.bufSize)
	n, err := r.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func (f *RawFramer) WriteFrame(w io.Writer, payload []byte) error {
	_, err := w.Write(payload)
	return err
}
