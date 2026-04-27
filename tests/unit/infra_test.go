package unit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/infra/bufferpool"
	"github.com/X1aSheng/shark-socket/internal/infra/cache"
	"github.com/X1aSheng/shark-socket/internal/infra/circuitbreaker"
	"github.com/X1aSheng/shark-socket/internal/infra/pubsub"
	"github.com/X1aSheng/shark-socket/internal/infra/store"
	"github.com/X1aSheng/shark-socket/internal/infra/tracing"
)

func TestBufferPool_GetPut(t *testing.T) {
	bp := bufferpool.New()

	buf := bp.Get(100)
	if buf == nil {
		t.Fatal("Get returned nil")
	}
	if buf.Len() != 100 {
		t.Fatalf("Len = %d, want 100", buf.Len())
	}
	copy(buf.Bytes(), []byte("test"))
	bp.Put(buf)
}

func TestBufferPool_Levels(t *testing.T) {
	bp := bufferpool.New()
	sizes := []int{64, 1024, 4096, 16384, 131072}
	for _, size := range sizes {
		buf := bp.Get(size)
		if buf == nil {
			t.Fatalf("Get(%d) returned nil", size)
		}
		if buf.Len() != size {
			t.Fatalf("Get(%d): Len = %d", size, buf.Len())
		}
		bp.Put(buf)
	}
}

func TestBufferPool_Stats(t *testing.T) {
	bp := bufferpool.New()
	// Drain the pool first to ensure a miss
	for i := 0; i < 10; i++ {
		buf := bp.Get(100)
		bp.Put(buf)
	}

	stats := bp.Stats()
	// At least one miss should have occurred on first Get
	t.Logf("Stats: hits=%v misses=%v allocs=%v totalMem=%d",
		stats.Hits, stats.Misses, stats.Allocs, stats.TotalMem)
}

func TestBufferPool_MemoryCap(t *testing.T) {
	bp := bufferpool.New()
	bp.SetTotalMemoryCap(1) // very low cap

	buf := bp.Get(100)
	bp.Put(buf) // should be discarded due to cap
}

func TestBufferPool_Default(t *testing.T) {
	buf := bufferpool.GetDefault(64)
	if buf == nil {
		t.Fatal("GetDefault returned nil")
	}
	bufferpool.PutDefault(buf)
}

func TestMemoryStore_CRUD(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemoryStore()

	if err := s.Save(ctx, "key1", []byte("value1")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	val, err := s.Load(ctx, "key1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(val) != "value1" {
		t.Fatalf("Load = %q, want %q", val, "value1")
	}

	if err := s.Delete(ctx, "key1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = s.Load(ctx, "key1")
	if err == nil {
		t.Fatal("Load after Delete should fail")
	}
}

func TestMemoryStore_Query(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemoryStore()
	s.Save(ctx, "user:1", []byte("alice"))
	s.Save(ctx, "user:2", []byte("bob"))
	s.Save(ctx, "item:1", []byte("widget"))

	results, err := s.Query(ctx, "user:")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Query(user:) = %d results, want 2", len(results))
	}
}

func TestChannelPubSub(t *testing.T) {
	ps := pubsub.NewChannelPubSub()

	received := make(chan []byte, 1)
	sub, err := ps.Subscribe(context.Background(), "topic1", func(data []byte) {
		received <- data
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := ps.Publish(context.Background(), "topic1", []byte("hello")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case data := <-received:
		if string(data) != "hello" {
			t.Fatalf("received %q, want %q", data, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}

	sub.Unsubscribe()
}

func TestChannelPubSub_Close(t *testing.T) {
	ps := pubsub.NewChannelPubSub()
	ps.Close()
	if err := ps.Publish(context.Background(), "topic", []byte("data")); err == nil {
		t.Fatal("Publish after Close should fail")
	}
}

func TestCircuitBreaker_Do(t *testing.T) {
	cb := circuitbreaker.New(3, 100*time.Millisecond)

	// Successful calls
	for i := 0; i < 5; i++ {
		if err := cb.Do(func() error { return nil }); err != nil {
			t.Fatalf("Do success %d: %v", i, err)
		}
	}

	// Record failures
	failErr := errors.New("fail")
	for i := 0; i < 3; i++ {
		cb.Do(func() error { return failErr })
	}

	// Should be open now
	if err := cb.Do(func() error { return nil }); err == nil {
		t.Fatal("Do should fail when circuit is open")
	}

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Should allow again (half-open)
	if err := cb.Do(func() error { return nil }); err != nil {
		t.Fatalf("Do after timeout: %v", err)
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := circuitbreaker.New(2, time.Minute)
	cb.Do(func() error { return errors.New("fail") })
	cb.Do(func() error { return errors.New("fail") })
	cb.Reset()

	if cb.Failures() != 0 {
		t.Fatalf("Failures after Reset = %d, want 0", cb.Failures())
	}
	if cb.State() != circuitbreaker.Closed {
		t.Fatalf("State after Reset = %v, want Closed", cb.State())
	}
}

func TestMemoryCache(t *testing.T) {
	ctx := context.Background()
	c := cache.NewMemoryCache()

	c.Set(ctx, "key1", []byte("val1"), 10*time.Second)
	val, err := c.Get(ctx, "key1")
	if err != nil || string(val) != "val1" {
		t.Fatalf("Get = %q, %v; want val1, nil", val, err)
	}

	c.Del(ctx, "key1")
	_, err = c.Get(ctx, "key1")
	if err == nil {
		t.Fatal("Get after Del should fail")
	}
}

func TestMemoryCache_TTL(t *testing.T) {
	ctx := context.Background()
	c := cache.NewMemoryCache()

	c.Set(ctx, "short", []byte("data"), 50*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	_, err := c.Get(ctx, "short")
	if err == nil {
		t.Fatal("Get after TTL should fail")
	}
}

func TestTracing_NopTracer(t *testing.T) {
	tracer := tracing.NopTracer{}
	// Using nil context intentionally for testing NopTracer behavior
	span, _ := tracer.StartSpan(context.TODO(), "test")
	if span == nil {
		t.Fatal("StartSpan returned nil span")
	}
	span.End()
	span.Context()
}

func TestTracing_SpanContext_RoundTrip(t *testing.T) {
	sc := tracing.SpanContext{
		TraceID: "trace-123",
		SpanID:  "span-456",
	}
	ctx := tracing.WithSpanContext(context.Background(), sc)
	got, ok := tracing.SpanFromContext(ctx)
	if !ok {
		t.Fatal("SpanFromContext returned false")
	}
	if got.TraceID != sc.TraceID || got.SpanID != sc.SpanID {
		t.Fatalf("got %+v, want %+v", got, sc)
	}
}

func TestTracing_WithAttribute(t *testing.T) {
	opt := tracing.WithAttribute("key", "value")
	cfg := &tracing.SpanConfig{}
	opt(cfg)
	if cfg.Attributes["key"] != "value" {
		t.Fatalf("Attribute = %q, want %q", cfg.Attributes["key"], "value")
	}
}
