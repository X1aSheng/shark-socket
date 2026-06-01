package coap

import (
	"testing"
)

func TestObserverRegistryRegisterAndNotify(t *testing.T) {
	reg := NewObserverRegistry()
	obs := reg.Register("/3/0/0", "192.168.1.1:1234", []byte{1, 2})
	if obs == nil {
		t.Fatal("expected observer")
	}
	results := reg.Notify("/3/0/0")
	if len(results) != 1 {
		t.Fatalf("want 1 observer, got %d", len(results))
	}
	if string(results[0].Token) != "\x01\x02" {
		t.Fatalf("token mismatch: %v", results[0].Token)
	}
}

func TestObserverRegistryRemove(t *testing.T) {
	reg := NewObserverRegistry()
	reg.Register("/3/0/0", "192.168.1.1:1234", []byte{1})
	reg.Remove("/3/0/0", "192.168.1.1:1234", []byte{1})
	if len(reg.Notify("/3/0/0")) != 0 {
		t.Fatal("expected 0 observers after remove")
	}
}

func TestObserverRegistryRemoveBySession(t *testing.T) {
	reg := NewObserverRegistry()
	reg.Register("/3/0/0", "192.168.1.1:1234", []byte{1})
	reg.Register("/3/0/1", "192.168.1.1:1234", []byte{2})
	reg.Register("/3/0/2", "192.168.1.2:5678", []byte{3})
	reg.RemoveBySession("192.168.1.1:1234")
	if len(reg.Notify("/3/0/0")) != 0 {
		t.Fatal("expected 0 for /3/0/0")
	}
	if len(reg.Notify("/3/0/1")) != 0 {
		t.Fatal("expected 0 for /3/0/1")
	}
	if len(reg.Notify("/3/0/2")) != 1 {
		t.Fatal("expected 1 for /3/0/2")
	}
}

func TestObserverNextSeq(t *testing.T) {
	obs := &Observer{}
	if obs.NextSeq() != 0 {
		t.Fatal("first seq must be 0")
	}
	if obs.NextSeq() != 1 {
		t.Fatal("second seq must be 1")
	}
}

func TestMessageWithObserveOption(t *testing.T) {
	msg := Message{
		Type:      TypeCON,
		Code:      CodeContent,
		MessageID: 7,
		Token:     []byte{1},
		Options:   map[uint16][]byte{ObserveOption: {0, 0, 0, 1}},
		Payload:   []byte("hello"),
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	val, ok := parsed.Options[ObserveOption]
	if !ok {
		t.Fatal("missing observe option")
	}
	if len(val) != 4 || val[3] != 1 {
		t.Fatalf("observe value = %v", val)
	}
	if string(parsed.Payload) != "hello" {
		t.Fatalf("payload = %s", string(parsed.Payload))
	}
}

func TestParseMessageWithoutOptions(t *testing.T) {
	msg := Message{Type: TypeCON, Code: CodeGet, MessageID: 1, Payload: []byte("test")}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(parsed.Payload) != "test" {
		t.Fatalf("payload = %s", string(parsed.Payload))
	}
}
