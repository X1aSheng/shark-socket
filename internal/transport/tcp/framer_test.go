package tcp

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestLengthPrefixFramerRoundTrip(t *testing.T) {
	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	var buf bytes.Buffer
	if err := framer.WriteFrame(&buf, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := framer.ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("payload = %q, want hello", got)
	}
}

func TestLineFramerRoundTrip(t *testing.T) {
	framer := LineFramer{MaxFrameBytes: 1024}
	var buf bytes.Buffer
	if err := framer.WriteFrame(&buf, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := framer.ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("payload = %q, want hello", got)
	}
}

func TestFixedSizeFramerRoundTrip(t *testing.T) {
	framer := FixedSizeFramer{Size: 5}
	var buf bytes.Buffer
	if err := framer.WriteFrame(&buf, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := framer.ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("payload = %q, want hello", got)
	}
}

func TestRawFramerRoundTrip(t *testing.T) {
	framer := RawFramer{MaxFrameBytes: 1024}
	var buf bytes.Buffer
	if err := framer.WriteFrame(&buf, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := framer.ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("payload = %q, want hello", got)
	}
}

func TestLengthPrefixFramerRejectsOversizedFrame(t *testing.T) {
	writer := LengthPrefixFramer{MaxFrameBytes: 1024}
	reader := LengthPrefixFramer{MaxFrameBytes: 3}
	var buf bytes.Buffer
	if err := writer.WriteFrame(&buf, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadFrame(&buf); !errors.Is(err, core.ErrFrameTooLarge) {
		t.Fatalf("ReadFrame error = %v, want %v", err, core.ErrFrameTooLarge)
	}
}

func FuzzLengthPrefixFramer(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte("x"), 128))
	f.Fuzz(func(t *testing.T, payload []byte) {
		framer := LengthPrefixFramer{MaxFrameBytes: 4096}
		var buf bytes.Buffer
		err := framer.WriteFrame(&buf, payload)
		if len(payload) > framer.MaxFrameBytes {
			if !errors.Is(err, core.ErrFrameTooLarge) {
				t.Fatalf("WriteFrame error = %v, want %v", err, core.ErrFrameTooLarge)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := framer.ReadFrame(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
		}
	})
}

func FuzzLineFramerRead(f *testing.F) {
	f.Add([]byte("hello\n"))
	f.Add([]byte("\n"))
	f.Add([]byte("missing delimiter"))
	f.Fuzz(func(t *testing.T, data []byte) {
		framer := LineFramer{MaxFrameBytes: 4096}
		_, err := framer.ReadFrame(bytes.NewReader(data))
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, core.ErrFrameTooLarge) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func BenchmarkLengthPrefixFramerRoundTrip(b *testing.B) {
	framer := LengthPrefixFramer{MaxFrameBytes: 4096}
	payload := bytes.Repeat([]byte("x"), 256)
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		if err := framer.WriteFrame(&buf, payload); err != nil {
			b.Fatal(err)
		}
		if _, err := framer.ReadFrame(&buf); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLineFramerRoundTrip(b *testing.B) {
	framer := LineFramer{MaxFrameBytes: 4096}
	payload := bytes.Repeat([]byte("x"), 256)
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		if err := framer.WriteFrame(&buf, payload); err != nil {
			b.Fatal(err)
		}
		if _, err := framer.ReadFrame(&buf); err != nil {
			b.Fatal(err)
		}
	}
}

func TestWithClientFramer(t *testing.T) {
	f := LengthPrefixFramer{MaxFrameBytes: 1024}
	opt := WithClientFramer(f)
	c := NewClient("127.0.0.1:0", opt)
	if c == nil {
		t.Fatal("client should not be nil")
	}
}

func TestWithClientTLS(t *testing.T) {
	opt := WithClientTLS(nil)
	c := NewClient("127.0.0.1:0", opt)
	if c == nil {
		t.Fatal("client should not be nil")
	}
}
