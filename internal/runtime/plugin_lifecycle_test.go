package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// lifecycleLog records ordered lifecycle events from plugins and servers.
type lifecycleLog struct {
	mu     sync.Mutex
	events []string
}

func (l *lifecycleLog) add(event string) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *lifecycleLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

// lifecyclePlugin is a core.LifecyclePlugin with observable Start/Stop.
type lifecyclePlugin struct {
	core.BasePlugin
	name     string
	priority int
	log      *lifecycleLog
	startErr error
	started  bool
	stopped  bool
}

func (p *lifecyclePlugin) Name() string  { return p.name }
func (p *lifecyclePlugin) Priority() int { return p.priority }
func (p *lifecyclePlugin) Start() error {
	if p.startErr != nil {
		p.log.add(p.name + ":start-error")
		return p.startErr
	}
	p.started = true
	p.log.add(p.name + ":start")
	return nil
}

func (p *lifecyclePlugin) Stop() error {
	p.stopped = true
	p.log.add(p.name + ":stop")
	return nil
}

// passivePlugin implements no Start/Stop and must be skipped by StartAll.
type passivePlugin struct {
	core.BasePlugin
	log *lifecycleLog
}

func (p *passivePlugin) Name() string  { return "passive" }
func (p *passivePlugin) Priority() int { return 10 }

// loggingServer records start/stop so tests can assert plugin/server ordering.
// It implements the staged shutdown contract, so the gateway calls
// CloseSessions (not Stop) on it during Gateway.Stop; the stop event is
// recorded there (Stop is kept for the non-staged path).
type loggingServer struct {
	proto core.Protocol
	log   *lifecycleLog
}

func (s *loggingServer) Protocol() core.Protocol { return s.proto }
func (s *loggingServer) Start(context.Context) error {
	s.log.add("server:start")
	return nil
}
func (s *loggingServer) Stop(context.Context) error {
	s.log.add("server:stop")
	return nil
}
func (s *loggingServer) StopAccept(context.Context) error    { return nil }
func (s *loggingServer) Drain(context.Context) error         { return nil }
func (s *loggingServer) CloseSessions(context.Context) error {
	s.log.add("server:stop")
	return nil
}

func TestGatewayStartsAndStopsLifecyclePlugins(t *testing.T) {
	log := &lifecycleLog{}
	low := &lifecyclePlugin{name: "low", priority: 50, log: log}
	high := &lifecyclePlugin{name: "high", priority: 100, log: log}
	passive := &passivePlugin{log: log}

	g := NewGateway(WithPlugins(low, high, passive))
	if err := g.Register(&loggingServer{proto: core.ProtocolTCP, log: log}); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := g.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	events := log.snapshot()
	want := []string{
		"low:start", "high:start", // plugins before servers, priority order
		"server:start",
		"server:stop",
		"high:stop", "low:stop", // plugins stopped after servers, reverse order
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestGatewayStartFailureRollsBackLifecyclePlugins(t *testing.T) {
	log := &lifecycleLog{}
	ok := &lifecyclePlugin{name: "ok", priority: 50, log: log}
	bad := &lifecyclePlugin{name: "bad", priority: 100, log: log, startErr: errors.New("boom")}

	g := NewGateway(WithPlugins(ok, bad))
	if err := g.Register(&loggingServer{proto: core.ProtocolTCP, log: log}); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded, want plugin start failure")
	}
	if !ok.stopped {
		t.Fatal("already-started plugin was not rolled back")
	}
	if g.Ready() {
		t.Fatal("gateway is ready after failed plugin start")
	}
	events := log.snapshot()
	want := []string{"ok:start", "bad:start-error", "ok:stop"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestGatewayServerStartFailureStopsLifecyclePlugins(t *testing.T) {
	log := &lifecycleLog{}
	plugin := &lifecyclePlugin{name: "p", priority: 50, log: log}
	g := NewGateway(WithPlugins(plugin))
	if err := g.Register(&fakeServer{proto: core.ProtocolTCP}); err != nil {
		t.Fatal(err)
	}
	if err := g.Register(&fakeServer{proto: core.ProtocolUDP, startErr: errors.New("boom")}); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded, want server start failure")
	}
	if !plugin.stopped {
		t.Fatal("plugin was not stopped after server start rollback")
	}
	events := log.snapshot()
	want := []string{"p:start", "p:stop"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestPluginChainStopAllWithoutStartIsSafe(t *testing.T) {
	log := &lifecycleLog{}
	plugin := &lifecyclePlugin{name: "p", priority: 50, log: log}
	chain := NewPluginChain(plugin)
	chain.StartAll() // starts p
	chain.StopAll()
	chain.StopAll() // double stop must not panic or double-close resources
	if !plugin.started || !plugin.stopped {
		t.Fatalf("plugin started=%v stopped=%v", plugin.started, plugin.stopped)
	}
}
