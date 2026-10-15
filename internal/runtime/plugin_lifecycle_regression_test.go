package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// panicLifecyclePlugin panics in Start and Stop so the chain's isolation can
// be exercised.
type panicLifecyclePlugin struct {
	name       string
	panicStart bool
	panicStop  bool
}

func (p *panicLifecyclePlugin) Name() string                                   { return p.name }
func (p *panicLifecyclePlugin) Priority() int                                  { return 1 }
func (p *panicLifecyclePlugin) OnAccept(core.Session) error                    { return nil }
func (p *panicLifecyclePlugin) OnMessage(core.Session, []byte) ([]byte, error) { return nil, nil }
func (p *panicLifecyclePlugin) OnClose(core.Session)                           {}
func (p *panicLifecyclePlugin) Start() error {
	if p.panicStart {
		panic("start boom")
	}
	return nil
}
func (p *panicLifecyclePlugin) Stop() error {
	if p.panicStop {
		panic("stop boom")
	}
	return nil
}

// TestStartAllIsolatesPluginPanic verifies that a plugin panicking in Start
// does not escape as a process-level crash. Every other plugin callback is
// already recovered by the chain; Start/Stop were the only unguarded ones, so
// a faulty lifecycle plugin took the whole process down during startup or
// mid-shutdown.
func TestStartAllIsolatesPluginPanic(t *testing.T) {
	chain := NewPluginChain(&panicLifecyclePlugin{name: "boom", panicStart: true})
	err := chain.StartAll()
	if err == nil {
		t.Fatal("StartAll with a panicking plugin returned nil")
	}
	if !errors.Is(err, core.ErrPluginPanic) {
		t.Fatalf("StartAll error = %v, want it to wrap core.ErrPluginPanic", err)
	}
}

// TestStopAllIsolatesPluginPanic verifies that a plugin panicking in Stop is
// contained. StopAll runs while Gateway.Stop holds startMu with sessions and
// listeners already closed; an escaping panic aborts the process mid-shutdown.
func TestStopAllIsolatesPluginPanic(t *testing.T) {
	chain := NewPluginChain(&panicLifecyclePlugin{name: "boom", panicStop: true})
	// Must not panic.
	chain.StopAll()
}

// TestStopAllReportsPluginStopError verifies that a plugin reporting a Stop
// failure is surfaced to the caller instead of being silently discarded:
// shutdown problems must be visible to operators.
func TestStopAllReportsPluginStopError(t *testing.T) {
	chain := NewPluginChain(&failingStopPlugin{name: "failing"})
	if err := chain.StopAll(); err == nil {
		t.Fatal("StopAll with a failing plugin returned nil; the error was discarded")
	}
}

type failingStopPlugin struct{ name string }

func (p *failingStopPlugin) Name() string                                   { return p.name }
func (p *failingStopPlugin) Priority() int                                  { return 1 }
func (p *failingStopPlugin) OnAccept(core.Session) error                    { return nil }
func (p *failingStopPlugin) OnMessage(core.Session, []byte) ([]byte, error) { return nil, nil }
func (p *failingStopPlugin) OnClose(core.Session)                           {}
func (p *failingStopPlugin) Start() error                                   { return nil }
func (p *failingStopPlugin) Stop() error                                    { return errors.New("stop failed") }

// panicSession is a session whose Close panics, used to check that Range
// isolates callback panics like Broadcast and CloseAll already do.
type panicSession struct {
	fakeSession
}

func (s *panicSession) Close(context.Context) error { panic("session close boom") }
