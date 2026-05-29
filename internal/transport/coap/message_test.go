package coap

import "testing"

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
