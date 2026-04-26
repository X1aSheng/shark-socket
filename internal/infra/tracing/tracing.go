// Package tracing provides a minimal, vendor-neutral tracing abstraction.
//
// The framework never imports OpenTelemetry (or any other tracing library)
// directly. Users who want distributed tracing provide their own [Tracer]
// implementation that wraps OTel, Jaeger, Zipkin, or any other backend.
//
// Basic usage with a custom tracer:
//
//	myTracer := &otelTracer{provider: provider} // implements tracing.Tracer
//	srv := tcp.NewServer(handler, tcp.WithTracer(myTracer))
package tracing

import "context"

// SpanContext carries distributed tracing identifiers across boundaries.
type SpanContext struct {
	TraceID string
	SpanID  string
}

// Span represents an in-flight span of work.
type Span interface {
	// End completes the span. Subsequent calls are no-ops.
	End()
	// Context returns the context.Context associated with this span,
	// which may carry trace propagation values.
	Context() context.Context
}

// Tracer is the minimal tracing interface that protocol servers accept.
// Implement this to integrate any distributed tracing backend.
type Tracer interface {
	// StartSpan creates a new span as a child of the span in ctx (if any).
	// The returned Span must be ended by calling Span.End().
	StartSpan(ctx context.Context, name string, opts ...SpanOption) (Span, context.Context)
}

// SpanOption is a functional option for span creation.
type SpanOption func(*SpanConfig)

// SpanConfig holds optional span configuration.
type SpanConfig struct {
	// Attributes are key-value pairs attached to the span.
	Attributes map[string]string
}

// WithAttribute adds a single attribute to the span configuration.
func WithAttribute(key, value string) SpanOption {
	return func(cfg *SpanConfig) {
		if cfg.Attributes == nil {
			cfg.Attributes = make(map[string]string)
		}
		cfg.Attributes[key] = value
	}
}

// --- Context helpers ---

type spanContextKey struct{}

// WithSpanContext attaches a SpanContext to the given context.
func WithSpanContext(ctx context.Context, sc SpanContext) context.Context {
	return context.WithValue(ctx, spanContextKey{}, sc)
}

// SpanFromContext retrieves the SpanContext from the given context.
// Returns false if no SpanContext is present.
func SpanFromContext(ctx context.Context) (SpanContext, bool) {
	sc, ok := ctx.Value(spanContextKey{}).(SpanContext)
	return sc, ok
}

// --- NopTracer ---

// NopTracer is a no-op tracer that discards all spans. It is safe for
// concurrent use and is the default when no tracer is configured.
type NopTracer struct{}

var _ Tracer = NopTracer{}

// StartSpan returns a nopSpan and the original context unchanged.
func (NopTracer) StartSpan(ctx context.Context, _ string, _ ...SpanOption) (Span, context.Context) {
	return nopSpan{ctx: ctx}, ctx
}

type nopSpan struct {
	ctx context.Context
}

func (nopSpan) End()                      {}
func (s nopSpan) Context() context.Context { return s.ctx }
