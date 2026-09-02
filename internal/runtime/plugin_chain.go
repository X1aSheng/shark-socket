package runtime

import (
	"fmt"
	"slices"
	"sync"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type PluginChain struct {
	mu      sync.RWMutex
	plugins []core.Plugin
	logger  core.Logger
}

func NewPluginChain(plugins ...core.Plugin) *PluginChain {
	c := &PluginChain{logger: core.NopLogger()}
	c.Append(plugins...)
	return c
}

// SetLogger sets the logger used for panic recovery logging.
// The write is locked so it is safe to call while the chain is serving.
func (c *PluginChain) SetLogger(logger core.Logger) {
	if logger == nil {
		return
	}
	c.mu.Lock()
	c.logger = logger
	c.mu.Unlock()
}

func (c *PluginChain) Append(plugins ...core.Plugin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Copy-on-write: build a new sorted slice and swap it in. c.plugins is
	// never mutated in place, so snapshot() can hand out the current slice
	// reference with no per-message copy.
	next := append([]core.Plugin(nil), c.plugins...)
	for _, p := range plugins {
		if p == nil {
			continue
		}
		next = append(next, p)
	}
	slices.SortFunc(next, func(a, b core.Plugin) int {
		return a.Priority() - b.Priority()
	})
	c.plugins = next
}

// StartAll starts every plugin that implements core.LifecyclePlugin, in
// priority order. If a plugin fails to start, the plugins started so far are
// stopped in reverse order and the failure is returned so Gateway.Start can
// abort. Passive plugins (no Start/Stop) are skipped. Repeated calls are safe:
// the built-in lifecycle-aware plugins treat a second Start as a no-op.
func (c *PluginChain) StartAll() error {
	plugins, _ := c.snapshot()
	var started []core.Plugin
	for _, p := range plugins {
		lp, ok := p.(core.LifecyclePlugin)
		if !ok {
			continue
		}
		if err := lp.Start(); err != nil {
			for i := len(started) - 1; i >= 0; i-- {
				if stop, ok := started[i].(core.LifecyclePlugin); ok {
					_ = stop.Stop()
				}
			}
			return fmt.Errorf("plugin %q start: %w", p.Name(), err)
		}
		started = append(started, p)
	}
	return nil
}

// StopAll stops every plugin that implements core.LifecyclePlugin, in reverse
// priority order. Safe to call when nothing was started (plugins tolerate
// Stop without Start) and from multiple goroutines.
func (c *PluginChain) StopAll() {
	plugins, _ := c.snapshot()
	for i := len(plugins) - 1; i >= 0; i-- {
		if lp, ok := plugins[i].(core.LifecyclePlugin); ok {
			_ = lp.Stop()
		}
	}
}

func (c *PluginChain) OnAccept(sess core.Session) error {
	plugins, logger := c.snapshot()
	for i, p := range plugins {
		if err := c.safeAccept(p, logger, sess); err != nil {
			// Roll back: already-accepted plugins receive OnClose (in reverse
			// order) so resources they allocated during OnAccept are released.
			for j := i - 1; j >= 0; j-- {
				c.safeClose(plugins[j], logger, sess)
			}
			return err
		}
	}
	return nil
}

func (c *PluginChain) OnMessage(sess core.Session, data []byte) ([]byte, error) {
	plugins, logger := c.snapshot()
	var err error
	for _, p := range plugins {
		data, err = c.safeMessage(p, logger, sess, data)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func (c *PluginChain) OnClose(sess core.Session) {
	plugins, logger := c.snapshot()
	for i := len(plugins) - 1; i >= 0; i-- {
		c.safeClose(plugins[i], logger, sess)
	}
}

// snapshot returns the current plugin slice (a stable, immutable copy-on-write
// view) and the current logger. Plugin callbacks run against this view, outside
// the RWMutex, so a plugin that calls SetLogger or Append from inside
// OnAccept/OnMessage/OnClose no longer deadlocks (RWMutex is not reentrant) and
// logger reads stay race-free even if SetLogger runs concurrently. Append only
// swaps in a new slice, so the returned slice needs no clone — zero allocation
// on the per-message OnMessage hot path.
func (c *PluginChain) snapshot() ([]core.Plugin, core.Logger) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.plugins, c.logger
}

func (c *PluginChain) safeAccept(p core.Plugin, logger core.Logger, sess core.Session) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("plugin accept panic", "plugin", p.Name(), "panic", r)
			err = core.ErrPluginPanic
		}
	}()
	return p.OnAccept(sess)
}

func (c *PluginChain) safeMessage(p core.Plugin, logger core.Logger, sess core.Session, data []byte) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("plugin message panic", "plugin", p.Name(), "panic", r)
			// Report the panic as an error so the transport drops/closes the
			// message path instead of treating it as a silent success.
			err = core.ErrPluginPanic
		}
	}()
	return p.OnMessage(sess, data)
}

func (c *PluginChain) safeClose(p core.Plugin, logger core.Logger, sess core.Session) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("plugin close panic", "plugin", p.Name(), "panic", r)
		}
	}()
	p.OnClose(sess)
}

var _ core.PluginRunner = (*PluginChain)(nil)
