package tcp

import (
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
