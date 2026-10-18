package runtime

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// pluginView is one immutable generation of the chain: the ordered plugin set
// plus the logger used for panic reporting. The chain never mutates a view in
// place; reconfiguration publishes a new one.
type pluginView struct {
	plugins []core.Plugin
	logger  core.Logger
}

type PluginChain struct {
	// mu guards publication of a new view only. Readers take no lock at all:
	// they load the current view pointer atomically. Before this, every
	// message took an RLock on one process-wide RWMutex, so all transports
	// and all cores contended for the same cache line on the hot path.
	mu   sync.Mutex
	view atomic.Pointer[pluginView]
}

func NewPluginChain(plugins ...core.Plugin) *PluginChain {
	c := &PluginChain{}
	c.view.Store(&pluginView{logger: core.NopLogger()})
	c.Append(plugins...)
	return c
}

// SetLogger sets the logger used for panic recovery logging. Safe to call
// while the chain is serving: it publishes a new view rather than mutating the
// one readers are using.
func (c *PluginChain) SetLogger(logger core.Logger) {
	if logger == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := c.view.Load()
	c.view.Store(&pluginView{plugins: cur.plugins, logger: logger})
}

func (c *PluginChain) Append(plugins ...core.Plugin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Copy-on-write: build a new sorted slice and publish it. The previous
	// view stays valid for readers that already loaded it, so a message in
	// flight sees one consistent generation instead of a slice being sorted
	// underneath it.
	cur := c.view.Load()
	next := append([]core.Plugin(nil), cur.plugins...)
	for _, p := range plugins {
		if p == nil {
			continue
		}
		next = append(next, p)
	}
	// Stable sort so plugins appended with equal priorities execute in append
	// order (a nondeterministic order would make equal-priority chains behave
	// differently between runs and platforms).
	slices.SortStableFunc(next, func(a, b core.Plugin) int {
		return a.Priority() - b.Priority()
	})
	c.view.Store(&pluginView{plugins: next, logger: cur.logger})
}

// current returns the published view. It is never nil: the constructor stores
// an empty view before the first Append.
func (c *PluginChain) current() *pluginView {
	if v := c.view.Load(); v != nil {
		return v
	}
	return &pluginView{logger: core.NopLogger()}
}

// StartAll starts every plugin that implements core.LifecyclePlugin, in
// priority order. If a plugin fails to start, the plugins started so far are
// stopped in reverse order and the failure is returned so Gateway.Start can
// abort. Passive plugins (no Start/Stop) are skipped. Repeated calls are safe:
// the built-in lifecycle-aware plugins treat a second Start as a no-op.
//
// Start is a user callback like any other, so it runs behind the same panic
// isolation as OnAccept/OnMessage/OnClose: an escaping panic here would bypass
// the gateway's error path and crash the process.
func (c *PluginChain) StartAll() error {
	v := c.current()
	var started []core.Plugin
	for _, p := range v.plugins {
		if _, ok := p.(core.LifecyclePlugin); !ok {
			continue
		}
		if err := c.safeStart(p, v.logger); err != nil {
			var rollbackErr error
			for i := len(started) - 1; i >= 0; i-- {
				// Rollback failures are reported alongside the original
				// start error: a plugin left running after a failed
				// startup is worth surfacing, not swallowing.
				rollbackErr = errors.Join(rollbackErr, c.safeStop(started[i], v.logger))
			}
			return errors.Join(fmt.Errorf("plugin %q start: %w", p.Name(), err), rollbackErr)
		}
		started = append(started, p)
	}
	return nil
}

// StopAll stops every plugin that implements core.LifecyclePlugin, in reverse
// priority order, and reports the aggregated failures. Safe to call when
// nothing was started (plugins tolerate Stop without Start) and from multiple
// goroutines.
func (c *PluginChain) StopAll() error {
	v := c.current()
	var err error
	for i := len(v.plugins) - 1; i >= 0; i-- {
		if _, ok := v.plugins[i].(core.LifecyclePlugin); ok {
			err = errors.Join(err, c.safeStop(v.plugins[i], v.logger))
		}
	}
	return err
}

// err is a named result because the deferred recover has to set it: without a
// name the panic path would return nil and a crashed plugin would look like a
// successful accept.
func (c *PluginChain) OnAccept(sess core.Session) (err error) {
	v := c.current()
	// One recover for the whole chain rather than one per plugin. OnAccept is
	// per connection, and each recover costs a defer record; the index below
	// identifies the offending plugin, which is all the old per-plugin
	// recover could report anyway.
	at := -1
	defer func() {
		if r := recover(); r != nil {
			v.logger.Error("plugin accept panic", "plugin", v.plugins[at].Name(), "panic", r)
			// Same rollback the error path performs: plugins that already
			// accepted must release whatever OnAccept allocated.
			for j := at - 1; j >= 0; j-- {
				c.safeClose(v.plugins[j], v.logger, sess)
			}
			err = core.ErrPluginPanic
		}
	}()
	for i, p := range v.plugins {
		at = i
		if err := p.OnAccept(sess); err != nil {
			// Roll back: already-accepted plugins receive OnClose (in reverse
			// order) so resources they allocated during OnAccept are released.
			for j := i - 1; j >= 0; j-- {
				c.safeClose(v.plugins[j], v.logger, sess)
			}
			return err
		}
	}
	return nil
}

func (c *PluginChain) OnMessage(sess core.Session, data []byte) (out []byte, err error) {
	v := c.current()
	out = data
	if len(v.plugins) == 0 {
		return out, nil
	}
	// One recover for the whole chain. OnMessage runs once per message, and
	// the previous design paid a defer record per plugin per message to
	// recover from a panic that has to abort the chain either way.
	at := -1
	defer func() {
		if r := recover(); r != nil {
			v.logger.Error("plugin message panic", "plugin", v.plugins[at].Name(), "panic", r)
			// Report the panic as an error so the transport drops/closes the
			// message path instead of treating it as a silent success.
			out, err = nil, core.ErrPluginPanic
		}
	}()
	for i, p := range v.plugins {
		at = i
		var e error
		out, e = p.OnMessage(sess, out)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}

func (c *PluginChain) OnClose(sess core.Session) {
	v := c.current()
	for i := len(v.plugins) - 1; i >= 0; i-- {
		c.safeClose(v.plugins[i], v.logger, sess)
	}
}

// safeClose keeps a per-plugin recover on purpose. OnClose must continue to the
// remaining plugins after one of them panics, otherwise a panicking plugin
// would strand the resources every later plugin owns.
func (c *PluginChain) safeClose(p core.Plugin, logger core.Logger, sess core.Session) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("plugin close panic", "plugin", p.Name(), "panic", r)
		}
	}()
	p.OnClose(sess)
}

func (c *PluginChain) safeStart(p core.Plugin, logger core.Logger) (err error) {
	lp := p.(core.LifecyclePlugin)
	defer func() {
		if r := recover(); r != nil {
			logger.Error("plugin start panic", "plugin", p.Name(), "panic", r)
			err = core.ErrPluginPanic
		}
	}()
	return lp.Start()
}

func (c *PluginChain) safeStop(p core.Plugin, logger core.Logger) (err error) {
	lp := p.(core.LifecyclePlugin)
	defer func() {
		if r := recover(); r != nil {
			logger.Error("plugin stop panic", "plugin", p.Name(), "panic", r)
			err = core.ErrPluginPanic
		}
	}()
	return lp.Stop()
}

var _ core.PluginRunner = (*PluginChain)(nil)
