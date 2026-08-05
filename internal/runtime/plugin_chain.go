package runtime

import (
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
func (c *PluginChain) SetLogger(logger core.Logger) {
	if logger != nil {
		c.logger = logger
	}
}

func (c *PluginChain) Append(plugins ...core.Plugin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range plugins {
		if p == nil {
			continue
		}
		c.plugins = append(c.plugins, p)
	}
	slices.SortFunc(c.plugins, func(a, b core.Plugin) int {
		return a.Priority() - b.Priority()
	})
}

func (c *PluginChain) OnAccept(sess core.Session) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i, p := range c.plugins {
		if err := c.safeAccept(p, sess); err != nil {
			// Roll back: already-accepted plugins receive OnClose (in reverse
			// order) so resources they allocated during OnAccept are released.
			for j := i - 1; j >= 0; j-- {
				c.safeClose(c.plugins[j], sess)
			}
			return err
		}
	}
	return nil
}

func (c *PluginChain) OnMessage(sess core.Session, data []byte) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var err error
	for _, p := range c.plugins {
		data, err = c.safeMessage(p, sess, data)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func (c *PluginChain) OnClose(sess core.Session) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := len(c.plugins) - 1; i >= 0; i-- {
		c.safeClose(c.plugins[i], sess)
	}
}

func (c *PluginChain) safeAccept(p core.Plugin, sess core.Session) (err error) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("plugin accept panic", "plugin", p.Name(), "panic", r)
			err = core.ErrPluginPanic
		}
	}()
	return p.OnAccept(sess)
}

func (c *PluginChain) safeMessage(p core.Plugin, sess core.Session, data []byte) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("plugin message panic", "plugin", p.Name(), "panic", r)
			out = data
			err = core.ErrPluginPanic
		}
	}()
	return p.OnMessage(sess, data)
}

func (c *PluginChain) safeClose(p core.Plugin, sess core.Session) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("plugin close panic", "plugin", p.Name(), "panic", r)
		}
	}()
	p.OnClose(sess)
}

var _ core.PluginRunner = (*PluginChain)(nil)
