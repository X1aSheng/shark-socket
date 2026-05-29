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
