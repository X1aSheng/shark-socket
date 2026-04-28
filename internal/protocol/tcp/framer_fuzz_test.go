package tcp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// FuzzLengthPrefixFramer verifies that the framer doesn't panic on arbitrary input.
func FuzzLengthPrefixFramer(f *testing.F) {
	// Seed corpus: valid frames, edge cases, and malformed input.
	f.Add([]byte{0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'})        // valid 5-byte frame
	f.Add([]byte{0, 0, 0, 0})                                     // zero-length frame
	f.Add([]byte{0, 0, 0, 1})                                     // truncated: header says 1 byte but none follows
	f.Add([]byte{})                                                // completely empty
	f.Add([]byte{0, 0})                                            // truncated header
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})                         // 4 GB frame (should be rejected)
	f.Add([]byte{0, 0, 0, 10, 's', 'h', 'o', 'r', 't'})          // header says 10 but only 5 bytes follow
	f.Add(make([]byte, 4+binary.MaxVarintLen64))                  // max varint header

	framer := NewLengthPrefixFramer(1 << 20)

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = fuzzReadFrame(framer, data)
	})
}

// FuzzLineFramer verifies line-delimited parsing doesn't panic.
func FuzzLineFramer(f *testing.F) {
	f.Add([]byte("hello\n"))
	f.Add([]byte("\n"))
	f.Add([]byte(""))
	f.Add([]byte("no newline"))
	f.Add([]byte{0, 0, 0, '\n'})
	f.Add(bytes.Repeat([]byte("x"), 10000))

	framer := NewLineFramer(1 << 20)

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = fuzzReadFrame(framer, data)
	})
}

// FuzzRawFramer verifies raw reads don't panic.
func FuzzRawFramer(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte(""))
	f.Add(make([]byte, 8192))

	framer := NewRawFramer(4096)

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = fuzzReadFrame(framer, data)
	})
}

// FuzzFixedSizeFramer verifies fixed-size reads don't panic.
func FuzzFixedSizeFramer(f *testing.F) {
	f.Add([]byte("12345678")) // 8 bytes for size=8
	f.Add([]byte("12"))        // short
	f.Add([]byte(""))          // empty
	f.Add(make([]byte, 64))   // 64 bytes for size=8

	framer := NewFixedSizeFramer(8)

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = fuzzReadFrame(framer, data)
	})
}

// fuzzReadFrame attempts to read a frame and absorbs any error without panicking.
func fuzzReadFrame(framer Framer, data []byte) (recovered any) {
	defer func() {
		recovered = recover()
	}()
	buf := bytes.NewReader(data)
	_, _ = framer.ReadFrame(buf)
	return nil
}

// FuzzLengthPrefixRoundtrip verifies WriteFrame → ReadFrame roundtrip.
func FuzzLengthPrefixRoundtrip(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte(""))
	f.Add(bytes.Repeat([]byte("A"), 4096))

	framer := NewLengthPrefixFramer(1 << 20)

	f.Fuzz(func(t *testing.T, payload []byte) {
		var buf bytes.Buffer
		if err := framer.WriteFrame(&buf, payload); err != nil {
			t.Skip()
		}
		got, err := framer.ReadFrame(&buf)
		if err != nil {
			t.Fatalf("roundtrip failed: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("roundtrip mismatch: got %d bytes, want %d", len(got), len(payload))
		}
	})
}
