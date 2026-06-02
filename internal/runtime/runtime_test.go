package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestNewRuntimeDefaults(t *testing.T) {
	r := NewRuntime(nil, nil)
	if r == nil {
		t.Fatal("runtime should not be nil")
	}
	if r.Sessions() == nil {
		t.Fatal("Sessions() should not be nil")
	}
	if r.Plugins() == nil {
		t.Fatal("Plugins() should not be nil")
	}
	if r.Logger() == nil {
		t.Fatal("Logger() should not be nil")
	}
	if r.Metrics() == nil {
		t.Fatal("Metrics() should not be nil")
	}
	if r.Tracer() == nil {
		t.Fatal("Tracer() should not be nil")
	}
}

func TestNewRuntimeWithOptions(t *testing.T) {
	sm := NewSessionManager()
	pc := NewPluginChain()
	logger := core.NopLogger()
	metrics := core.NopMetrics()
	tracer := core.NopTracer()

	r := NewRuntime(sm, pc,
		withRuntimeLogger(logger),
		withRuntimeMetrics(metrics),
		withRuntimeTracer(tracer),
	)
	if r == nil {
		t.Fatal("runtime should not be nil")
	}
	if r.Sessions() != sm {
		t.Fatal("Sessions() should return the passed manager")
	}
	if r.Plugins() != pc {
		t.Fatal("Plugins() should return the passed chain")
	}
}

func TestRuntimeLoggerMetricsTracer(t *testing.T) {
	r := NewRuntime(nil, nil)
	r.Logger().Info("test")
	r.Metrics().IncCounter("test_counter", "key", "val")
	ctx, span := r.Tracer().Start(context.Background(), "span")
	if ctx == nil {
		t.Fatal("context should not be nil")
	}
	span.End()
	// These are no-ops with nop implementations
}

func TestOptionsDirectCoverage(t *testing.T) {
	// Test the options directly for coverage
	sm := NewSessionManager()
	opt1 := WithSessionManager(sm)
	cfg := &gatewayConfig{}
	opt1(cfg)
	if cfg.sessions != sm {
		t.Fatal("WithSessionManager should set sessions")
	}

	opt2 := WithPlugins()
	opt2(cfg)
	if cfg.plugins == nil {
		t.Fatal("WithPlugins should set plugins")
	}

	opt3 := WithLogger(core.NopLogger())
	opt3(cfg)
	if cfg.logger == nil {
		t.Fatal("WithLogger should set logger")
	}

	opt4 := WithMetrics(core.NopMetrics())
	opt4(cfg)
	if cfg.metrics == nil {
		t.Fatal("WithMetrics should set metrics")
	}

	opt5 := WithTracer(core.NopTracer())
	opt5(cfg)
	if cfg.tracer == nil {
		t.Fatal("WithTracer should set tracer")
	}

	opt6 := WithStageTimeouts(core.StageTimeouts{
		StopAccept:    3 * time.Second,
		Drain:         3 * time.Second,
		CloseSessions: 3 * time.Second,
		Finalize:      3 * time.Second,
	})
	opt6(cfg)
	if cfg.timeouts.StopAccept != 3*time.Second {
		t.Fatalf("timeouts.StopAccept = %v, want 3s", cfg.timeouts.StopAccept)
	}
}

func TestNormalizeTimeouts(t *testing.T) {
	// All zeros -> use defaults
	zeroTimeouts := core.StageTimeouts{}
	normalized := normalizeTimeouts(zeroTimeouts)
	if normalized.StopAccept == 0 {
		t.Fatal("default StopAccept should not be 0")
	}
	if normalized.Drain == 0 {
		t.Fatal("default Drain should not be 0")
	}
	// Custom values should be preserved
	custom := core.StageTimeouts{
		StopAccept:    1 * time.Second,
		Drain:         2 * time.Second,
		CloseSessions: 3 * time.Second,
		Finalize:      4 * time.Second,
	}
	norm2 := normalizeTimeouts(custom)
	if norm2.StopAccept != 1*time.Second {
		t.Fatalf("custom StopAccept should be preserved")
	}
}

func TestGatewayHealth(t *testing.T) {
	gw := NewGateway()
	health := gw.Health()
	if health == nil {
		t.Fatal("Health() should not return nil")
	}
	if started, ok := health["started"].(bool); !ok || started {
		t.Fatal("unstarted gateway health should have started=false")
	}
}

func TestGatewayProtocols(t *testing.T) {
	gw := NewGateway()
	protocols := gw.Protocols()
	// Empty gateway returns empty (not nil) slice
	if len(protocols) != 0 {
		t.Fatalf("unregistered gateway should have 0 protocols, got %d", len(protocols))
	}
}

func TestGatewayRuntime(t *testing.T) {
	gw := NewGateway()
	r := gw.Runtime()
	if r == nil {
		t.Fatal("Runtime() should not return nil")
	}
}

func TestSessionManagerGet(t *testing.T) {
	sm := NewSessionManager()
	if _, ok := sm.Get(9999); ok {
		t.Fatal("Get non-existent should return false")
	}
}

func TestPluginChainOnMessage(t *testing.T) {
	pc := NewPluginChain()
	data, err := pc.OnMessage(nil, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("empty chain OnMessage should pass through: %q", data)
	}
}

func TestPluginChainOnMessageWithPlugins(t *testing.T) {
	pc := NewPluginChain(&testPrefixPlugin{prefix: "X:"})
	data, err := pc.OnMessage(nil, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "X:hello" {
		t.Fatalf("plugin should prefix: %q", data)
	}
}

func TestPluginChainOnClose(t *testing.T) {
	pc := NewPluginChain(&testClosePlugin{})
	// Should not panic
	pc.OnClose(nil)
	// Should be safe with empty chain
	NewPluginChain().OnClose(nil)
}

type testPrefixPlugin struct {
	core.BasePlugin
	prefix string
}

func (p testPrefixPlugin) Name() string { return "test-prefix" }

func (p testPrefixPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	return append([]byte(p.prefix), data...), nil
}

type testClosePlugin struct {
	core.BasePlugin
}

func (p testClosePlugin) Name() string { return "test-close" }

func TestGatewayHealthStarted(t *testing.T) {
	// Integration test: gateway health after start
	gw := NewGateway(WithPlugins(&testPrefixPlugin{prefix: "G:"}))
	r := gw.Runtime()
	if r == nil {
		t.Fatal("runtime should not be nil")
	}
	health := gw.Health()
	protocols, ok := health["protocols"]
	if !ok {
		t.Fatal("health should include protocols")
	}
	if len(protocols.([]core.Protocol)) != 0 {
		t.Fatal("protocols should be empty for unstarted gateway")
	}
}

func TestRuntimeWithCustomLogger(t *testing.T) {
	logger := core.NopLogger()
	r := NewRuntime(nil, nil, withRuntimeLogger(logger))
	// Verify logger was set - calling it should not panic
	r.Logger().Info("hello")
}
