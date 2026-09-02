package pubsub

import (
	"testing"
)

// TestPubSubDroppedCounterCleanedWithLastSubscriber verifies that the dropped
// counter of a topic does not leak once its last subscriber leaves.
func TestPubSubDroppedCounterCleanedWithLastSubscriber(t *testing.T) {
	p := New()

	ch, cancel := p.Subscribe("topic-a", 1)
	// Fill the buffer, then overflow it: the second publish is dropped.
	p.Publish("topic-a", []byte("one"))
	if dropped := p.Dropped("topic-a"); dropped != 0 {
		t.Fatalf("dropped = %d, want 0 before overflow", dropped)
	}
	p.Publish("topic-a", []byte("two"))
	if dropped := p.Dropped("topic-a"); dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}

	cancel()
	if dropped := p.Dropped("topic-a"); dropped != 0 {
		t.Fatalf("dropped after cancel = %d, want 0 (counter leaked)", dropped)
	}

	// The channel is closed; publishing to the abandoned topic is a no-op.
	p.Publish("topic-a", []byte("three"))
	_ = ch
	if dropped := p.Dropped("topic-a"); dropped != 0 {
		t.Fatalf("dropped after republish = %d, want 0", dropped)
	}
}
