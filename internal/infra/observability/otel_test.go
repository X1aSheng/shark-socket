package observability

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestOpenTelemetryTracerRecordsSpanAttributesAndErrors(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer := NewOpenTelemetryTracer(provider.Tracer("shark-socket-new-test"))

	_, span := tracer.Start(context.Background(), "runtime.message", "protocol", "tcp", "session_id", int64(7), "accepted", true)
	span.RecordError(errors.New("boom"))
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if spans[0].Name() != "runtime.message" {
		t.Fatalf("span name = %q", spans[0].Name())
	}
	attrs := spans[0].Attributes()
	assertAttr(t, attrs, "protocol", "tcp")
	assertAttr(t, attrs, "session_id", "7")
	assertAttr(t, attrs, "accepted", "true")
	if len(spans[0].Events()) == 0 {
		t.Fatal("expected recorded error event")
	}
}

func TestOpenTelemetryTracerNilFallsBackToNoop(t *testing.T) {
	tracer := NewOpenTelemetryTracer(nil)
	_, span := tracer.Start(context.Background(), "noop")
	span.RecordError(errors.New("ignored"))
	span.End()
}

func assertAttr(t *testing.T, attrs []attribute.KeyValue, key, want string) {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == key && fmt.Sprint(attr.Value.AsInterface()) == want {
			return
		}
	}
	t.Fatalf("attribute %s=%s not found in %#v", key, want, attrs)
}
