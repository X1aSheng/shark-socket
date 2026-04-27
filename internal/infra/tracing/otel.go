package tracing

import (
	"context"
	"log"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// OTelTracer is a Tracer implementation backed by OpenTelemetry.
type OTelTracer struct {
	tracer    oteltrace.Tracer
	propagator propagation.TextMapPropagator
	exporter  sdktrace.SpanExporter // kept for lifecycle management
}

// OTelOption configures the OTelTracer.
type OTelOption func(*OTelConfig)

type OTelConfig struct {
	ServiceName    string
	ServiceVersion string
	Exporter       sdktrace.SpanExporter
	Propagator     propagation.TextMapPropagator
	Sampler        sdktrace.Sampler
}

// WithServiceName sets the service name for all spans.
func WithServiceName(name string) OTelOption {
	return func(c *OTelConfig) { c.ServiceName = name }
}

// WithServiceVersion sets the service version.
func WithServiceVersion(v string) OTelOption {
	return func(c *OTelConfig) { c.ServiceVersion = v }
}

// WithExporter sets the trace exporter.
func WithExporter(exp sdktrace.SpanExporter) OTelOption {
	return func(c *OTelConfig) { c.Exporter = exp }
}

// WithPropagator sets the propagation format (e.g., W3C TraceContext, B3).
func WithPropagator(prop propagation.TextMapPropagator) OTelOption {
	return func(c *OTelConfig) { c.Propagator = prop }
}

// WithSampler sets the sampling strategy.
func WithSampler(s sdktrace.Sampler) OTelOption {
	return func(c *OTelConfig) { c.Sampler = s }
}

// NewOTelTracer creates a new OpenTelemetry-backed tracer.
// It registers a TracerProvider if an exporter is provided.
func NewOTelTracer(opts ...OTelOption) (*OTelTracer, error) {
	cfg := OTelConfig{
		ServiceName: "shark-socket",
		Propagator:  otel.GetTextMapPropagator(),
		Sampler:     sdktrace.ParentBased(sdktrace.AlwaysSample()),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	ot := &OTelTracer{
		propagator: cfg.Propagator,
		exporter:   cfg.Exporter,
	}

	if cfg.Exporter != nil {
		// Create a TracerProvider with the exporter
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(cfg.Exporter),
			sdktrace.WithSampler(cfg.Sampler),
		)
		otel.SetTracerProvider(tp)

		// Also set global propagator if configured
		if cfg.Propagator != nil {
			otel.SetTextMapPropagator(cfg.Propagator)
		}
	}

	ot.tracer = otel.Tracer(cfg.ServiceName)

	return ot, nil
}

// StartSpan implements Tracer.StartSpan.
func (t *OTelTracer) StartSpan(ctx context.Context, name string, spanOpts ...SpanOption) (Span, context.Context) {
	// Extract parent span context from ctx using propagator
	parentCtx := t.propagator.Extract(ctx, propagation.HeaderCarrier{})

	// Convert our SpanOption to OTel span options
	var attrs []attribute.KeyValue
	for _, opt := range spanOpts {
		cfg := &SpanConfig{}
		opt(cfg)
		for k, v := range cfg.Attributes {
			attrs = append(attrs, attribute.String(k, v))
		}
	}

	_, span := t.tracer.Start(parentCtx, name,
		oteltrace.WithAttributes(attrs...),
		oteltrace.WithAttributes(
			attribute.String("service.name", "shark-socket"),
		),
	)

	// Create context with span context for propagation
	newCtx := oteltrace.ContextWithSpan(ctx, span)
	return &otelSpan{inner: span}, newCtx
}

// SpanFromOtelSpan wraps an OTel span to implement our Span interface.
type otelSpan struct {
	inner oteltrace.Span
}

func (s *otelSpan) End() {
	s.inner.End()
}

func (s *otelSpan) Context() context.Context {
	return oteltrace.ContextWithSpan(context.Background(), s.inner)
}

// Shutdown gracefully shuts down the tracer provider.
func (t *OTelTracer) Shutdown(ctx context.Context) error {
	if tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); ok {
		return tp.Shutdown(ctx)
	}
	return nil
}

// OTelSpanContext converts our SpanContext to OTel format.
func OTelSpanContext(ctx context.Context) SpanContext {
	span := oteltrace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		return SpanContext{
			TraceID: span.SpanContext().TraceID().String(),
			SpanID:  span.SpanContext().SpanID().String(),
		}
	}
	return SpanContext{}
}

// InjectOTelContext injects trace context into a carrier using the propagator.
func InjectOTelContext(ctx context.Context, carrier map[string]string) context.Context {
	span := oteltrace.SpanFromContext(ctx)
	spanCtx := span.SpanContext()
	if spanCtx.IsValid() {
		carrier["trace_id"] = spanCtx.TraceID().String()
		carrier["span_id"] = spanCtx.SpanID().String()
	}
	return ctx
}

// WrapOTelTracer wraps an existing oteltrace.Tracer to implement our Tracer interface.
func WrapOTelTracer(inner oteltrace.Tracer, propagator propagation.TextMapPropagator) Tracer {
	if propagator == nil {
		propagator = otel.GetTextMapPropagator()
	}
	return &otelTracerAdapter{tracer: inner, propagator: propagator}
}

type otelTracerAdapter struct {
	tracer     oteltrace.Tracer
	propagator propagation.TextMapPropagator
}

func (a *otelTracerAdapter) StartSpan(ctx context.Context, name string, opts ...SpanOption) (Span, context.Context) {
	parentCtx := a.propagator.Extract(ctx, propagation.HeaderCarrier{})

	var attrs []attribute.KeyValue
	for _, opt := range opts {
		cfg := &SpanConfig{}
		opt(cfg)
		for k, v := range cfg.Attributes {
			attrs = append(attrs, attribute.String(k, v))
		}
	}

	_, span := a.tracer.Start(parentCtx, name,
		oteltrace.WithAttributes(attrs...),
	)
	return &otelSpan{inner: span}, oteltrace.ContextWithSpan(ctx, span)
}

// --- W3C Propagation helpers ---

// W3CPropagator returns the standard W3C TraceContext propagator.
func W3CPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

// LogExporter is a simple trace exporter that logs spans.
// Useful for debugging and development.
type LogExporter struct{}

// ExportSpans exports spans by logging them.
func (LogExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		log.Printf("[trace] %s %s trace=%s span=%s",
			span.Name(),
			span.SpanContext().TraceState().String(),
			span.SpanContext().TraceID().String(),
			span.SpanContext().SpanID().String(),
		)
	}
	return nil
}

// Shutdown implements the exporter interface (no-op for log exporter).
func (LogExporter) Shutdown(ctx context.Context) error { return nil }