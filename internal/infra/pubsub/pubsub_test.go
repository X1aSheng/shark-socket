package pubsub

import (
	"sync"
	"testing"
)

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

// TestPubSubConcurrentPublishWithDrops exercises concurrent Publish (with
// drops) and Dropped() reads; the dropped counter must be written under the
// exclusive lock or the map races and the process crashes.
func TestPubSubConcurrentPublishWithDrops(t *testing.T) {
	ps := New()
	// A buffer-0 subscription means every publish drops, exercising the counter
	// write on the hot path.
	_, cancel := ps.Subscribe("topic", 0)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				ps.Publish("topic", []byte("x"))
				_ = ps.Dropped("topic")
			}
		}()
	}
	wg.Wait()
	if ps.Dropped("topic") == 0 {
		t.Fatal("expected dropped counter to accumulate")
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
