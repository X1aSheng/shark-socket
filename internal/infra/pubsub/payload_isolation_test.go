package pubsub

import (
	"testing"
	"time"
)

// TestPublishPayloadIsolatedPerSubscriber verifies that each subscriber gets
// its own copy of the payload. Sharing one backing array means a subscriber
// that mutates the message it received silently corrupts what every other
// subscriber sees, and it also breaks the isolation the copy on Publish
// intends to provide.
func TestPublishPayloadIsolatedPerSubscriber(t *testing.T) {
	p := New()
	ch1, cancel1 := p.Subscribe("t", 1)
	defer cancel1()
	ch2, cancel2 := p.Subscribe("t", 1)
	defer cancel2()

	if n := p.Publish("t", []byte("abc")); n != 2 {
		t.Fatalf("delivered = %d, want 2", n)
	}

	recv := func(ch <-chan Message) Message {
		select {
		case m := <-ch:
			return m
		case <-time.After(time.Second):
			t.Fatal("no message delivered")
			return Message{}
		}
	}
	m1 := recv(ch1)
	m2 := recv(ch2)

	m1.Data[0] = 'X'
	if string(m2.Data) != "abc" {
		t.Fatalf("subscriber 2 payload = %q after subscriber 1 mutated its copy, want abc (shared backing array)", m2.Data)
	}
}
