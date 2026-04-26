package tracing

import (
	"context"
	"testing"
)

func TestNopTracer_StartSpan(t *testing.T) {
	tracer := NopTracer{}
	ctx := context.Background()

	span, ctx2 := tracer.StartSpan(ctx, "test-operation")
	if span == nil {
		t.Fatal("expected non-nil span from NopTracer")
	}

	// End should be a no-op and not panic.
	span.End()

	// Context should be unchanged.
	if ctx2 != ctx {
		t.Error("NopTracer should return the original context")
	}
}

func TestNopTracer_WithAttributes(t *testing.T) {
	tracer := NopTracer{}
	ctx := context.Background()

	span, _ := tracer.StartSpan(ctx, "op", WithAttribute("key", "value"))
	span.End()
	// No panic = success for NopTracer.
}

func TestNopSpan_MultipleEnd(t *testing.T) {
	span := nopSpan{ctx: context.Background()}
	span.End()
	span.End() // second call should not panic
}

func TestNopSpan_Context(t *testing.T) {
	ctx := context.WithValue(context.Background(), "testkey", "testval")
	span := nopSpan{ctx: ctx}

	got := span.Context()
	if got != ctx {
		t.Error("nopSpan should return the original context")
	}

	val := got.Value("testkey")
	if val != "testval" {
		t.Errorf("context value mismatch: got %v, want %v", val, "testval")
	}
}

func TestWithSpanContext_RoundTrip(t *testing.T) {
	ctx := context.Background()

	sc := SpanContext{
		TraceID: "abc123",
		SpanID:  "def456",
	}

	ctx = WithSpanContext(ctx, sc)

	retrieved, ok := SpanFromContext(ctx)
	if !ok {
		t.Fatal("expected SpanContext in context")
	}
	if retrieved.TraceID != sc.TraceID {
		t.Errorf("TraceID: got %q, want %q", retrieved.TraceID, sc.TraceID)
	}
	if retrieved.SpanID != sc.SpanID {
		t.Errorf("SpanID: got %q, want %q", retrieved.SpanID, sc.SpanID)
	}
}

func TestSpanFromContext_Missing(t *testing.T) {
	ctx := context.Background()

	_, ok := SpanFromContext(ctx)
	if ok {
		t.Error("expected no SpanContext in empty context")
	}
}

func TestSpanFromContext_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), spanContextKey{}, "not-a-spancontext")

	_, ok := SpanFromContext(ctx)
	if ok {
		t.Error("expected no SpanContext when value has wrong type")
	}
}

func TestWithSpanContext_Overwrite(t *testing.T) {
	ctx := context.Background()

	sc1 := SpanContext{TraceID: "first", SpanID: "first"}
	sc2 := SpanContext{TraceID: "second", SpanID: "second"}

	ctx = WithSpanContext(ctx, sc1)
	ctx = WithSpanContext(ctx, sc2)

	retrieved, ok := SpanFromContext(ctx)
	if !ok {
		t.Fatal("expected SpanContext in context")
	}
	if retrieved.TraceID != "second" {
		t.Errorf("expected overwritten TraceID %q, got %q", "second", retrieved.TraceID)
	}
}

func TestWithAttribute(t *testing.T) {
	cfg := &SpanConfig{}
	opt := WithAttribute("foo", "bar")
	opt(cfg)

	if cfg.Attributes == nil {
		t.Fatal("expected attributes map to be initialized")
	}
	if cfg.Attributes["foo"] != "bar" {
		t.Errorf("expected attribute foo=bar, got %q", cfg.Attributes["foo"])
	}
}

func TestWithAttribute_Multiple(t *testing.T) {
	cfg := &SpanConfig{}
	WithAttribute("a", "1")(cfg)
	WithAttribute("b", "2")(cfg)

	if len(cfg.Attributes) != 2 {
		t.Errorf("expected 2 attributes, got %d", len(cfg.Attributes))
	}
}
