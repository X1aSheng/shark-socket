package runtime

import (
	"log/slog"
	"slices"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type PluginChain struct {
	plugins []core.Plugin
}

func NewPluginChain(plugins ...core.Plugin) *PluginChain {
	c := &PluginChain{}
	c.Append(plugins...)
	return c
}

func (c *PluginChain) Append(plugins ...core.Plugin) {
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
	for _, p := range c.plugins {
		if err := safeAccept(p, sess); err != nil {
			return err
		}
	}
	return nil
}

func (c *PluginChain) OnMessage(sess core.Session, data []byte) ([]byte, error) {
	var err error
	for _, p := range c.plugins {
		data, err = safeMessage(p, sess, data)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func (c *PluginChain) OnClose(sess core.Session) {
	for i := len(c.plugins) - 1; i >= 0; i-- {
		safeClose(c.plugins[i], sess)
	}
}

func safeAccept(p core.Plugin, sess core.Session) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("plugin accept panic", "plugin", p.Name(), "panic", r)
			err = core.ErrPluginPanic
		}
	}()
	return p.OnAccept(sess)
}

func safeMessage(p core.Plugin, sess core.Session, data []byte) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("plugin message panic", "plugin", p.Name(), "panic", r)
			out = data
			err = core.ErrPluginPanic
		}
	}()
	return p.OnMessage(sess, data)
}

func safeClose(p core.Plugin, sess core.Session) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("plugin close panic", "plugin", p.Name(), "panic", r)
		}
	}()
	p.OnClose(sess)
}

var _ core.PluginRunner = (*PluginChain)(nil)
