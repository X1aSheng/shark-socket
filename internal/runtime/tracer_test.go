package runtime

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// recordingTracer stands in for a real tracer so a test can tell an injected
// one from the default nop: the two are otherwise indistinguishable through
// core.Tracer, and every call is a no-op in both.
type recordingTracer struct {
	starts atomic.Int64
}

func (t *recordingTracer) Start(ctx context.Context, name string, attrs ...any) (context.Context, core.Span) {
	t.starts.Add(1)
	return core.NopTracer().Start(ctx, name, attrs...)
}

// tracingServer captures the runtime the gateway injects, which is the only
// path by which a transport ever sees a tracer.
type tracingServer struct {
	rt core.Runtime
}

func (s *tracingServer) Protocol() core.Protocol     { return core.ProtocolTCP }
func (s *tracingServer) Start(context.Context) error { return nil }
func (s *tracingServer) Stop(context.Context) error  { return nil }
func (s *tracingServer) UseRuntime(rt core.Runtime)  { s.rt = rt }

// TestGatewayInjectsTheConfiguredTracer is the only guarantee behind the
// tracer extension point: the runtime itself emits no spans (ADR-021), so if
// this wiring broke, nothing else in the repository would notice -- no
// transport calls Tracer(), and the app never configures one.
func TestGatewayInjectsTheConfiguredTracer(t *testing.T) {
	want := &recordingTracer{}
	g := NewGateway(WithTracer(want))
	server := &tracingServer{}
	if err := g.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Stop(context.Background()) }()

	if server.rt == nil {
		t.Fatal("gateway did not call UseRuntime on the transport")
	}
	got := server.rt.Tracer()
	if got != core.Tracer(want) {
		t.Fatalf("transport sees tracer %T, want the configured %T", got, want)
	}

	// A span created through the injected tracer must reach it: the value
	// being equal is not enough, the interface must dispatch to it.
	_, span := got.Start(context.Background(), "test.span")
	if span == nil {
		t.Fatal("Start returned a nil span")
	}
	span.End()
	if n := want.starts.Load(); n != 1 {
		t.Fatalf("tracer saw %d Start calls, want 1", n)
	}
}

// TestGatewayDefaultsToNopTracer pins the default: a gateway built without
// WithTracer still hands transports a usable tracer. Returning nil here would
// panic the first embedder who calls Start on it.
func TestGatewayDefaultsToNopTracer(t *testing.T) {
	g := NewGateway()
	server := &tracingServer{}
	if err := g.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Stop(context.Background()) }()

	if server.rt == nil || server.rt.Tracer() == nil {
		t.Fatal("default tracer is nil; a nil tracer panics on first use")
	}
	ctx, span := server.rt.Tracer().Start(context.Background(), "noop")
	if ctx == nil || span == nil {
		t.Fatal("nop tracer must return the original context and a non-nil span")
	}
	span.RecordError(nil) // must not panic
	span.End()
}
