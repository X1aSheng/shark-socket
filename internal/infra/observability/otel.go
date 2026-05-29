package observability

import (
	"context"
	"fmt"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type OpenTelemetryTracer struct {
	tracer trace.Tracer
}

type otelSpan struct {
	span trace.Span
}

func NewOpenTelemetryTracer(tracer trace.Tracer) *OpenTelemetryTracer {
	return &OpenTelemetryTracer{tracer: tracer}
}

func (t *OpenTelemetryTracer) Start(ctx context.Context, name string, attrs ...any) (context.Context, core.Span) {
	if t == nil || t.tracer == nil {
		return core.NopTracer().Start(ctx, name, attrs...)
	}
	ctx, span := t.tracer.Start(ctx, name, trace.WithAttributes(otelAttributes(attrs...)...))
	return ctx, otelSpan{span: span}
}

func (s otelSpan) End() {
	s.span.End()
}

func (s otelSpan) RecordError(err error) {
	if err == nil {
		return
	}
	s.span.RecordError(err)
	s.span.SetStatus(codes.Error, err.Error())
}

func otelAttributes(attrs ...any) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]attribute.KeyValue, 0, (len(attrs)+1)/2)
	for i := 0; i < len(attrs); i += 2 {
		key := fmt.Sprint(attrs[i])
		if key == "" {
			key = "attr"
		}
		if i+1 >= len(attrs) {
			out = append(out, attribute.String(key, ""))
			continue
		}
		out = append(out, otelAttribute(key, attrs[i+1]))
	}
	return out
}

func otelAttribute(key string, value any) attribute.KeyValue {
	switch v := value.(type) {
	case string:
		return attribute.String(key, v)
	case bool:
		return attribute.Bool(key, v)
	case int:
		return attribute.Int(key, v)
	case int64:
		return attribute.Int64(key, v)
	case float64:
		return attribute.Float64(key, v)
	default:
		return attribute.String(key, fmt.Sprint(v))
	}
}

var _ core.Tracer = (*OpenTelemetryTracer)(nil)
