package runtime

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// panicPlugin panics from the callback named by stage.
type panicPlugin struct {
	core.BasePlugin
	name     string
	priority int
	stage    string
	closed   *[]string
}

func (p panicPlugin) Name() string  { return p.name }
func (p panicPlugin) Priority() int { return p.priority }

func (p panicPlugin) OnAccept(core.Session) error {
	if p.stage == "accept" {
		panic("accept boom")
	}
	return nil
}

func (p panicPlugin) OnMessage(core.Session, []byte) ([]byte, error) {
	if p.stage == "message" {
		panic("message boom")
	}
	return nil, nil
}

func (p panicPlugin) OnClose(core.Session) {
	if p.closed != nil {
		*p.closed = append(*p.closed, p.name)
	}
	if p.stage == "close" {
		panic("close boom")
	}
}

// TestPluginChainMessagePanicBecomesError pins the observable contract of the
// chain-level recover: a panicking plugin surfaces as core.ErrPluginPanic, not
// as a crash and not as a silent success. It is the same value the old
// per-plugin recover produced.
func TestPluginChainMessagePanicBecomesError(t *testing.T) {
	chain := NewPluginChain(
		panicPlugin{name: "ok", priority: 1},
		panicPlugin{name: "boom", priority: 2, stage: "message"},
		panicPlugin{name: "never", priority: 3},
	)
	out, err := chain.OnMessage(nil, []byte("payload"))
	if !errors.Is(err, core.ErrPluginPanic) {
		t.Fatalf("OnMessage err = %v, want core.ErrPluginPanic", err)
	}
	if out != nil {
		t.Errorf("OnMessage returned %q, want nil after a panic", out)
	}
}

// TestPluginChainAcceptPanicRollsBack pins the rollback that the old
// per-plugin recover performed through OnAccept's error path: plugins that
// already accepted must still receive OnClose, or whatever they allocated for
// the connection is stranded.
func TestPluginChainAcceptPanicRollsBack(t *testing.T) {
	var closed []string
	chain := NewPluginChain(
		panicPlugin{name: "ok", priority: 1, closed: &closed},
		panicPlugin{name: "boom", priority: 2, stage: "accept", closed: &closed},
		panicPlugin{name: "never", priority: 3, closed: &closed},
	)
	if err := chain.OnAccept(nil); !errors.Is(err, core.ErrPluginPanic) {
		t.Fatalf("OnAccept err = %v, want core.ErrPluginPanic", err)
	}
	// The panicking plugin must not leak what the plugins before it
	// allocated, and the plugins after it were never reached.
	if len(closed) != 1 || closed[0] != "ok" {
		t.Errorf("OnClose calls = %v, want exactly [ok]", closed)
	}
}

// TestPluginChainClosePanicContinues pins the reason OnClose keeps a per-plugin
// recover: unlike OnMessage and OnAccept, a panic here must NOT abort the rest
// of the chain, because every later plugin owns resources that are only
// released by its own OnClose.
func TestPluginChainClosePanicContinues(t *testing.T) {
	var closed []string
	chain := NewPluginChain(
		panicPlugin{name: "first", priority: 1, closed: &closed},
		panicPlugin{name: "boom", priority: 2, stage: "close", closed: &closed},
		panicPlugin{name: "last", priority: 3, closed: &closed},
	)
	chain.OnClose(nil)
	// Reverse priority order, and "first" must still be reached after "boom".
	if len(closed) != 3 || closed[0] != "last" || closed[1] != "boom" || closed[2] != "first" {
		t.Errorf("OnClose calls = %v, want [last boom first]", closed)
	}
}

// TestPluginChainReconfigureUnderLoad is the race-detector companion to the
// lock-free read path: readers never take the mutex, so a concurrent
// SetLogger/Append must remain both deadlock-free and race-free.
func TestPluginChainReconfigureUnderLoad(t *testing.T) {
	chain := NewPluginChain(panicPlugin{name: "p1", priority: 1})
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(3)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			chain.SetLogger(core.NopLogger())
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			chain.Append(panicPlugin{name: "p2", priority: 2})
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := chain.OnMessage(nil, []byte("x")); err != nil {
				t.Errorf("OnMessage err = %v, want nil", err)
				return
			}
		}
	}()

	time.Sleep(200 * time.Millisecond)
	close(stop)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("plugin chain deadlocked under concurrent reconfiguration")
	}
}

// TestPluginChainViewNeverNil guards the read path against a zero-value
// PluginChain: every method must tolerate a chain built without the
// constructor, which is what a struct literal in a test or an embedding
// package would produce.
func TestPluginChainViewNeverNil(t *testing.T) {
	var chain PluginChain
	if _, err := chain.OnMessage(nil, []byte("x")); err != nil {
		t.Errorf("OnMessage on a zero chain err = %v, want nil", err)
	}
	if err := chain.OnAccept(nil); err != nil {
		t.Errorf("OnAccept on a zero chain err = %v, want nil", err)
	}
	chain.OnClose(nil)
}
