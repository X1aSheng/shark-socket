package pubsub

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPublishSubscribe_BasicCommunication(t *testing.T) {
	ps := NewChannelPubSub()
	defer ps.Close()

	var received atomic.Value
	sub, err := ps.Subscribe(context.Background(), "test-topic", func(data []byte) {
		received.Store(string(data))
	})
	if err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}
	defer sub.Unsubscribe()

	payload := []byte("hello world")
	if err := ps.Publish(context.Background(), "test-topic", payload); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	got, ok := received.Load().(string)
	if !ok {
		t.Fatal("handler was not called")
	}
	if got != "hello world" {
		t.Fatalf("expected %q, got %q", "hello world", got)
	}
}

func TestPublishSubscribe_MultipleSubscribersFanOut(t *testing.T) {
	ps := NewChannelPubSub()
	defer ps.Close()

	var wg sync.WaitGroup
	var mu sync.Mutex
	received := make([]string, 0, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		sub, err := ps.Subscribe(context.Background(), "fanout", func(data []byte) {
			mu.Lock()
			received = append(received, string(data))
			mu.Unlock()
			wg.Done()
		})
		if err != nil {
			t.Fatalf("Subscribe %d returned error: %v", i, err)
		}
		defer sub.Unsubscribe()
	}

	if err := ps.Publish(context.Background(), "fanout", []byte("broadcast")); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatalf("expected 3 deliveries, got %d", len(received))
	}
	for _, msg := range received {
		if msg != "broadcast" {
			t.Fatalf("expected %q, got %q", "broadcast", msg)
		}
	}
}

func TestUnsubscribe_StopsDelivery(t *testing.T) {
	ps := NewChannelPubSub()
	defer ps.Close()

	var count atomic.Int32

	sub, err := ps.Subscribe(context.Background(), "unsub-topic", func(data []byte) {
		count.Add(1)
	})
	if err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}

	if err := ps.Publish(context.Background(), "unsub-topic", []byte("first")); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	sub.Unsubscribe()

	if err := ps.Publish(context.Background(), "unsub-topic", []byte("second")); err != nil {
		t.Fatalf("Publish after unsubscribe returned error: %v", err)
	}

	if got := count.Load(); got != 1 {
		t.Fatalf("expected handler to be called once, got %d", got)
	}
}

func TestClose_RejectsFurtherOperations(t *testing.T) {
	ps := NewChannelPubSub()

	// Subscribe before close so we know it works.
	_, err := ps.Subscribe(context.Background(), "pre-close", func(data []byte) {})
	if err != nil {
		t.Fatalf("Subscribe before close returned error: %v", err)
	}

	ps.Close()

	// Publish after close should fail.
	if err := ps.Publish(context.Background(), "pre-close", []byte("data")); err != ErrPubSubClosed {
		t.Fatalf("expected ErrPubSubClosed, got %v", err)
	}

	// Subscribe after close should fail.
	_, err = ps.Subscribe(context.Background(), "post-close", func(data []byte) {})
	if err != ErrPubSubClosed {
		t.Fatalf("expected ErrPubSubClosed, got %v", err)
	}
}

func TestPublish_NoSubscribers_NoError(t *testing.T) {
	ps := NewChannelPubSub()
	defer ps.Close()

	err := ps.Publish(context.Background(), "nobody-listening", []byte("data"))
	if err != nil {
		t.Fatalf("Publish to topic with no subscribers returned error: %v", err)
	}
}
