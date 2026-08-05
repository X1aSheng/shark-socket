package runtime

import (
	"errors"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type orderPlugin struct {
	core.BasePlugin
	name     string
	priority int
	order    *[]string
}

func (p orderPlugin) Name() string  { return p.name }
func (p orderPlugin) Priority() int { return p.priority }
func (p orderPlugin) OnAccept(core.Session) error {
	*p.order = append(*p.order, p.name)
	return nil
}

func TestPluginChainSortsByPriority(t *testing.T) {
	var order []string
	chain := NewPluginChain(
		orderPlugin{name: "late", priority: 20, order: &order},
		orderPlugin{name: "early", priority: 10, order: &order},
	)

	if err := chain.OnAccept(nil); err != nil {
		t.Fatal(err)
	}
	if got := order[0]; got != "early" {
		t.Fatalf("first plugin = %q, want early", got)
	}
}

type recordingPlugin struct {
	core.BasePlugin
	name       string
	accepted   bool
	closed     bool
	failAccept bool
}

func (p *recordingPlugin) Name() string { return p.name }

func (p *recordingPlugin) OnAccept(core.Session) error {
	if p.failAccept {
		return core.ErrPluginBlock
	}
	p.accepted = true
	return nil
}

func (p *recordingPlugin) OnClose(core.Session) {
	p.closed = true
}

// TestPluginChainOnAcceptFailureRollsBackClose verifies that when plugin N
// fails OnAccept, all already-accepted plugins receive OnClose.
func TestPluginChainOnAcceptFailureRollsBackClose(t *testing.T) {
	good := &recordingPlugin{name: "good"}
	failing := &recordingPlugin{name: "failing", failAccept: true}
	chain := NewPluginChain(good, failing)

	if err := chain.OnAccept(nil); !errors.Is(err, core.ErrPluginBlock) {
		t.Fatalf("OnAccept error = %v, want %v", err, core.ErrPluginBlock)
	}
	if !good.accepted {
		t.Fatal("first plugin was not accepted")
	}
	if !good.closed {
		t.Fatal("already-accepted plugin did not receive OnClose after later OnAccept failure")
	}
}
