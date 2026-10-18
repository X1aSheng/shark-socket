package tcp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

func FuzzFixedSizeFramerRead(f *testing.F) {
	f.Add([]byte("12345"))
	f.Add([]byte(""))
	f.Add(make([]byte, 100))

	f.Fuzz(func(t *testing.T, data []byte) {
		framer := FixedSizeFramer{Size: 5}
		got, err := framer.ReadFrame(bytes.NewReader(data))
		if err != nil && got == nil {
			return
		}
		// May get partial read if data is shorter than frame size
		_ = got
	})
}

func FuzzRawFramerRoundTrip(f *testing.F) {
	f.Add([]byte("raw data"))
	f.Add([]byte{})
	f.Add(make([]byte, 1024))

	f.Fuzz(func(t *testing.T, payload []byte) {
		framer := RawFramer{}
		var buf bytes.Buffer
		if err := framer.WriteFrame(&buf, payload); err != nil {
			t.Fatal(err)
		}
		got, err := framer.ReadFrame(&buf)
		if len(payload) == 0 && errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("raw framer roundtrip failed")
		}
	})
}

// FuzzBufferedFramerRead exercises the *bufio.Reader fast paths with an
// arbitrarily small buffer, which is where the interesting cases live: a
// delimiter split across buffer refills, a length header that arrives in two
// pieces, and a stream that ends mid-frame.
func FuzzBufferedFramerRead(f *testing.F) {
	f.Add([]byte("hello\nworld\n"), uint8(16))
	f.Add([]byte{0, 0, 0, 3, 'a', 'b', 'c'}, uint8(16))
	f.Add([]byte{0, 0, 0, 0}, uint8(16))
	f.Add([]byte("no-delimiter-at-all"), uint8(16))
	f.Add([]byte{}, uint8(16))

	f.Fuzz(func(t *testing.T, data []byte, size uint8) {
		bufSize := int(size)
		if bufSize < 16 {
			bufSize = 16 // bufio's minimum
		}

		lf := LengthPrefixFramer{MaxFrameBytes: 4096}
		br := bufio.NewReaderSize(bytes.NewReader(data), bufSize)
		for i := 0; i < 16; i++ {
			p, err := lf.ReadFrame(br)
			if err != nil {
				break
			}
			if len(p) > 4096 {
				t.Fatalf("length-prefix frame escaped MaxFrameBytes: %d", len(p))
			}
		}

		line := LineFramer{MaxFrameBytes: 512}
		br = bufio.NewReaderSize(bytes.NewReader(data), bufSize)
		for i := 0; i < 16; i++ {
			p, err := line.ReadFrame(br)
			if err != nil {
				break
			}
			if len(p) > 512 {
				t.Fatalf("line frame escaped MaxFrameBytes: %d", len(p))
			}
		}
	})
}
