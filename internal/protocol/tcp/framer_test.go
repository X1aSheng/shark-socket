package tcp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/yourname/shark-socket/internal/errs"
)

// --- LengthPrefixFramer ---

func TestLengthPrefixFramer_WriteThenRead(t *testing.T) {
	f := NewLengthPrefixFramer(1024)
	payload := []byte("hello world")

	var buf bytes.Buffer
	if err := f.WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := f.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload mismatch: got %q, want %q", got, payload)
	}
}

func TestLengthPrefixFramer_EmptyPayload(t *testing.T) {
	f := NewLengthPrefixFramer(1024)

	var buf bytes.Buffer
	if err := f.WriteFrame(&buf, []byte{}); err != nil {
		t.Fatalf("WriteFrame empty: %v", err)
	}

	got, err := f.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(got))
	}
}

func TestLengthPrefixFramer_LargePayload(t *testing.T) {
	maxSize := 4096
	f := NewLengthPrefixFramer(maxSize)

	payload := bytes.Repeat([]byte("A"), maxSize)
	var buf bytes.Buffer
	if err := f.WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame large: %v", err)
	}

	got, err := f.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame large: %v", err)
	}
	if len(got) != maxSize {
		t.Errorf("payload length: got %d, want %d", len(got), maxSize)
	}
}

func TestLengthPrefixFramer_FrameTooLarge(t *testing.T) {
	f := NewLengthPrefixFramer(128)

	var buf bytes.Buffer
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(256))
	buf.Write(header)
	buf.Write(bytes.Repeat([]byte("X"), 256))

	_, err := f.ReadFrame(&buf)
	if err == nil {
		t.Fatal("expected error for oversized frame")
	}
}

func TestLengthPrefixFramer_TruncatedFrame(t *testing.T) {
	f := NewLengthPrefixFramer(1024)

	var buf bytes.Buffer
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(100))
	buf.Write(header)
	buf.Write(bytes.Repeat([]byte("Y"), 10))

	_, err := f.ReadFrame(&buf)
	if err == nil {
		t.Fatal("expected error for truncated frame")
	}
}

func TestLengthPrefixFramer_ReadEmpty(t *testing.T) {
	f := NewLengthPrefixFramer(1024)
	var buf bytes.Buffer
	_, err := f.ReadFrame(&buf)
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected EOF, got %v", err)
	}
}

// TestLengthPrefixFramer_MultipleFrames uses io.Pipe to avoid
// bufio.Reader buffering ahead and consuming data from a bytes.Buffer.
func TestLengthPrefixFramer_MultipleFrames(t *testing.T) {
	f := NewLengthPrefixFramer(4096)
	frames := [][]byte{
		[]byte("first"),
		[]byte("second"),
		[]byte("third"),
	}

	pr, pw := io.Pipe()
	go func() {
		for _, frame := range frames {
			if err := f.WriteFrame(pw, frame); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		pw.Close()
	}()

	for i, expected := range frames {
		got, err := f.ReadFrame(pr)
		if err != nil {
			t.Fatalf("ReadFrame[%d]: %v", i, err)
		}
		if string(got) != string(expected) {
			t.Errorf("frame[%d]: got %q, want %q", i, got, expected)
		}
	}

	// After all frames, next read should be EOF
	_, err := f.ReadFrame(pr)
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected EOF after all frames, got %v", err)
	}
}

func TestLengthPrefixFramer_MaxSizeZero(t *testing.T) {
	// maxSize=0 means no limit check
	f := NewLengthPrefixFramer(0)
	payload := bytes.Repeat([]byte("Z"), 2048)

	var buf bytes.Buffer
	if err := f.WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := f.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if len(got) != 2048 {
		t.Errorf("expected 2048 bytes, got %d", len(got))
	}
}

// --- LineFramer ---

func TestLineFramer_WriteThenRead(t *testing.T) {
	f := NewLineFramer(1024)
	payload := []byte("hello")

	var buf bytes.Buffer
	if err := f.WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := f.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload mismatch: got %q, want %q", got, payload)
	}
}

// TestLineFramer_MultipleLines uses io.Pipe for streaming.
func TestLineFramer_MultipleLines(t *testing.T) {
	f := NewLineFramer(4096)
	lines := [][]byte{
		[]byte("line1"),
		[]byte("line2"),
		[]byte("line3"),
	}

	pr, pw := io.Pipe()
	go func() {
		for _, line := range lines {
			if err := f.WriteFrame(pw, line); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		pw.Close()
	}()

	for i, expected := range lines {
		got, err := f.ReadFrame(pr)
		if err != nil {
			t.Fatalf("ReadFrame[%d]: %v", i, err)
		}
		if string(got) != string(expected) {
			t.Errorf("line[%d]: got %q, want %q", i, got, expected)
		}
	}
}

func TestLineFramer_ReadWithoutNewline(t *testing.T) {
	f := NewLineFramer(1024)
	var buf bytes.Buffer
	buf.Write([]byte("no newline here"))

	_, err := f.ReadFrame(&buf)
	if err == nil {
		t.Fatal("expected error reading line without newline")
	}
}

// --- FixedSizeFramer ---

func TestFixedSizeFramer_WriteThenRead(t *testing.T) {
	size := 8
	f := NewFixedSizeFramer(size)
	payload := []byte("12345678")

	var buf bytes.Buffer
	if err := f.WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := f.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload mismatch: got %q, want %q", got, payload)
	}
}

func TestFixedSizeFramer_Truncated(t *testing.T) {
	size := 16
	f := NewFixedSizeFramer(size)

	var buf bytes.Buffer
	buf.Write([]byte("short"))

	_, err := f.ReadFrame(&buf)
	if err == nil {
		t.Fatal("expected error for truncated fixed-size frame")
	}
}

func TestFixedSizeFramer_MultipleFrames(t *testing.T) {
	size := 4
	f := NewFixedSizeFramer(size)

	var buf bytes.Buffer
	data := []byte("AAAABBBBCCCC")
	if err := f.WriteFrame(&buf, data); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	for i := 0; i < 3; i++ {
		got, err := f.ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame[%d]: %v", i, err)
		}
		expected := data[i*size : (i+1)*size]
		if string(got) != string(expected) {
			t.Errorf("frame[%d]: got %q, want %q", i, got, expected)
		}
	}
}

// --- RawFramer ---

func TestRawFramer_WriteThenRead(t *testing.T) {
	f := NewRawFramer(4096)
	payload := []byte("raw data here")

	var buf bytes.Buffer
	if err := f.WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := f.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload mismatch: got %q, want %q", got, payload)
	}
}

func TestRawFramer_DefaultBufSize(t *testing.T) {
	f := NewRawFramer(0)
	if f.bufSize != 4096 {
		t.Errorf("expected default bufSize 4096, got %d", f.bufSize)
	}
}

func TestRawFramer_NegativeBufSize(t *testing.T) {
	f := NewRawFramer(-1)
	if f.bufSize != 4096 {
		t.Errorf("expected default bufSize 4096 for negative input, got %d", f.bufSize)
	}
}

func TestRawFramer_ReadEmpty(t *testing.T) {
	f := NewRawFramer(4096)
	var buf bytes.Buffer
	_, err := f.ReadFrame(&buf)
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected EOF, got %v", err)
	}
}

// --- Framer interface compile-time check ---

func TestFramerInterface(t *testing.T) {
	var _ Framer = NewLengthPrefixFramer(1024)
	var _ Framer = NewLineFramer(1024)
	var _ Framer = NewFixedSizeFramer(64)
	var _ Framer = NewRawFramer(4096)
}

// --- errs package reference ---

func TestErrsPackageReference(t *testing.T) {
	_ = errs.ErrFrameTooLarge
	_ = errs.ErrInvalidFrame
}
