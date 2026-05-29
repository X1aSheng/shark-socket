package coap

import (
	"bytes"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	msg := Message{Type: TypeCON, Code: CodePost, MessageID: 42, Token: []byte{1, 2}, Payload: []byte("hello")}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != msg.Type || got.Code != msg.Code || got.MessageID != msg.MessageID || string(got.Payload) != "hello" {
		t.Fatalf("parsed message mismatch: %#v", got)
	}
}

func TestParseRejectsInvalidVersion(t *testing.T) {
	if _, err := Parse([]byte{0, CodeGet, 0, 1}); err != ErrInvalidVersion {
		t.Fatalf("Parse error = %v, want %v", err, ErrInvalidVersion)
	}
}

func FuzzParseMessage(f *testing.F) {
	seed, err := (Message{Type: TypeCON, Code: CodePost, MessageID: 42, Token: []byte{1}, Payload: []byte("hello")}).Marshal()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{Version << 6, CodeGet, 0, 1})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		msg, err := Parse(data)
		if err != nil {
			return
		}
		encoded, err := msg.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		got, err := Parse(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != msg.Type || got.Code != msg.Code || got.MessageID != msg.MessageID || !bytes.Equal(got.Token, msg.Token) || !bytes.Equal(got.Payload, msg.Payload) {
			t.Fatalf("roundtrip mismatch: got %#v want %#v", got, msg)
		}
	})
}

func BenchmarkMessageParse(b *testing.B) {
	data, err := (Message{Type: TypeCON, Code: CodePost, MessageID: 42, Token: []byte{1, 2}, Payload: bytes.Repeat([]byte("x"), 256)}).Marshal()
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		if _, err := Parse(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMessageMarshal(b *testing.B) {
	msg := Message{Type: TypeCON, Code: CodePost, MessageID: 42, Token: []byte{1, 2}, Payload: bytes.Repeat([]byte("x"), 256)}
	for i := 0; i < b.N; i++ {
		if _, err := msg.Marshal(); err != nil {
			b.Fatal(err)
		}
	}
}
