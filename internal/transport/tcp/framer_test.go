package tcp

import (
	"bytes"
	"errors"
	"testing"

	"github.com/X1aSheng/shark-socket-new/internal/core"
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
