package runtime

import (
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
