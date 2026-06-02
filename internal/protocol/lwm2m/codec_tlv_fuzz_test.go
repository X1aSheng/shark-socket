package lwm2m

import (
	"bytes"
	"testing"
)

func FuzzTLVRoundTrip(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte{})
	f.Add(make([]byte, 256))

	f.Fuzz(func(t *testing.T, payload []byte) {
		entry := tlvEntry{id: 0, typ: ResourceString, value: payload}
		encoded, err := EncodeTLV([]tlvEntry{entry})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeTLV(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if len(decoded) != 1 {
			t.Fatalf("expected 1 resource, got %d", len(decoded))
		}
		if !bytes.Equal(decoded[0].value, payload) {
			t.Fatalf("value mismatch for %d-byte payload", len(payload))
		}
	})
}

func FuzzTLVDecodeRandom(f *testing.F) {
	f.Add([]byte{0xC1, 0, 0, 0, 4, 0, 0, 0, 42})
	f.Add([]byte{})
	f.Add([]byte{0xFF, 0xFF, 0, 0})
	f.Add(make([]byte, 256))

	f.Fuzz(func(t *testing.T, data []byte) {
		// DecodeTLV should never panic on arbitrary input
		decoded, err := DecodeTLV(data)
		if err != nil {
			return // expected for invalid data
		}
		_ = decoded
	})
}
