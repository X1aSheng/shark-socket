package types

import (
	"testing"
	"time"
)

func TestNewRawMessage(t *testing.T) {
	payload := []byte("hello")
	msg := NewRawMessage(42, TCP, payload)

	if msg.ID == "" {
		t.Error("message ID should not be empty")
	}
	if msg.SessionID != 42 {
		t.Errorf("SessionID = %d, want 42", msg.SessionID)
	}
	if msg.Protocol != TCP {
		t.Errorf("Protocol = %v, want TCP", msg.Protocol)
	}
	if string(msg.Payload) != "hello" {
		t.Errorf("Payload = %q, want %q", msg.Payload, "hello")
	}
	if msg.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
	if msg.Metadata == nil {
		t.Error("Metadata should be initialized")
	}
	if len(msg.Metadata) != 0 {
		t.Errorf("Metadata should be empty initially, got %d items", len(msg.Metadata))
	}
}

func TestNewRawMessageIDsAreUnique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		msg := NewRawMessage(1, TCP, nil)
		if ids[msg.ID] {
			t.Errorf("duplicate message ID: %s", msg.ID)
		}
		ids[msg.ID] = true
	}
}

func TestMessageGeneric(t *testing.T) {
	msg := Message[string]{
		ID:        "test",
		SessionID: 1,
		Protocol:  HTTP,
		Payload:   "hello",
		Timestamp: time.Now(),
	}
	if msg.Payload != "hello" {
		t.Errorf("Payload = %q, want %q", msg.Payload, "hello")
	}
}

func TestRawMessageAlias(t *testing.T) {
	var _ RawMessage = Message[[]byte]{}
	var msg RawMessage = NewRawMessage(1, TCP, []byte("test"))
	if string(msg.Payload) != "test" {
		t.Error("RawMessage alias should work")
	}
}
