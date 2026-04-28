package plugin

import (
	"log"
	"slices"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/infra/metrics"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// Chain executes plugins in priority order with ErrSkip/ErrDrop/ErrBlock semantics.
type Chain struct {
	plugins     []types.Plugin
	nameIndex   map[string]int
	stopOnError bool
}

// NewChain creates a plugin chain sorted by priority (ascending).
func NewChain(plugins ...types.Plugin) *Chain {
	c := &Chain{
		plugins:     make([]types.Plugin, 0, len(plugins)),
		nameIndex:   make(map[string]int),
		stopOnError: true,
	}
	for _, p := range plugins {
		c.Add(p)
	}
	return c
}

// WithStopOnError configures whether the chain stops on non-control errors.
func (c *Chain) WithStopOnError(stop bool) *Chain {
	c.stopOnError = stop
	return c
}

// Add inserts a plugin, deduplicating by name.
func (c *Chain) Add(p types.Plugin) {
	name := p.Name()
	if idx, ok := c.nameIndex[name]; ok {
		c.plugins[idx] = p
	} else {
		c.nameIndex[name] = len(c.plugins)
		c.plugins = append(c.plugins, p)
	}
	slices.SortFunc(c.plugins, func(a, b types.Plugin) int {
		return a.Priority() - b.Priority()
	})
	for i, p := range c.plugins {
		c.nameIndex[p.Name()] = i
	}
}

// OnAccept executes plugins in order on connection acceptance.
func (c *Chain) OnAccept(sess types.RawSession) error {
	for _, p := range c.plugins {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[shark-socket] plugin %s OnAccept panic: %v", p.Name(), r)
					metrics.IncCounter("shark_worker_panics_total", p.Name())
					err = nil
				}
			}()
			start := time.Now()
			err = p.OnAccept(sess)
			metrics.ObserveHistogram("shark_plugin_duration_seconds", time.Since(start).Seconds(), p.Name())
		}()
		if err != nil {
			if err == errs.ErrBlock {
				_ = sess.Close()
				return errs.ErrBlock
			}
			if c.stopOnError {
				_ = sess.Close()
				return err
			}
		}
	}
	return nil
}

// OnMessage executes plugins in order, threading data through the chain.
func (c *Chain) OnMessage(sess types.RawSession, data []byte) ([]byte, error) {
	for _, p := range c.plugins {
		var out []byte
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[shark-socket] plugin %s OnMessage panic: %v", p.Name(), r)
					metrics.IncCounter("shark_worker_panics_total", p.Name())
					out = data // pass through unchanged
					err = nil
				}
			}()
			start := time.Now()
			out, err = p.OnMessage(sess, data)
			metrics.ObserveHistogram("shark_plugin_duration_seconds", time.Since(start).Seconds(), p.Name())
		}()
		if err != nil {
			switch err {
			case errs.ErrSkip:
				return data, nil
			case errs.ErrDrop:
				return nil, errs.ErrDrop
			default:
				if c.stopOnError {
					return nil, err
				}
				continue
			}
		}
		data = out
	}
	return data, nil
}

// OnClose executes plugins in reverse order, always calling all.
func (c *Chain) OnClose(sess types.RawSession) {
	for i := len(c.plugins) - 1; i >= 0; i-- {
		p := c.plugins[i]
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[shark-socket] plugin %s OnClose panic: %v", p.Name(), r)
					metrics.IncCounter("shark_worker_panics_total", p.Name())
				}
			}()
			p.OnClose(sess)
		}()
	}
}

// Len returns the number of plugins.
func (c *Chain) Len() int { return len(c.plugins) }
