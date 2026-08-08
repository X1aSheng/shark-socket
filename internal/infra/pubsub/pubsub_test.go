package pubsub

import "testing"

func TestPubSubPublishSubscribe(t *testing.T) {
	ps := New()
	ch, cancel := ps.Subscribe("topic", 1)
	defer cancel()
	if delivered := ps.Publish("topic", []byte("hello")); delivered != 1 {
		t.Fatalf("delivered = %d, want 1", delivered)
	}
	msg := <-ch
	if msg.Topic != "topic" || string(msg.Data) != "hello" {
		t.Fatalf("message = %#v", msg)
	}
}

// TestPubSubTopicKeyRemovedAfterLastCancel verifies that the map key is dropped
// when the last subscriber leaves, so transient topics do not accumulate
// empty-slice entries forever.
func TestPubSubTopicKeyRemovedAfterLastCancel(t *testing.T) {
	ps := New()
	_, cancel := ps.Subscribe("topic", 1)
	cancel()
	ps.mu.RLock()
	_, exists := ps.subs["topic"]
	ps.mu.RUnlock()
	if exists {
		t.Fatal("topic key should be removed after last subscriber cancels")
	}
}
