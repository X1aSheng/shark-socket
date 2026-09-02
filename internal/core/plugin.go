package core

import "errors"

var (
	ErrPluginDrop  = errors.New("plugin drop")
	ErrPluginBlock = errors.New("plugin block")
)

// Plugin observes or transforms the session lifecycle.
type Plugin interface {
	Name() string
	Priority() int
	OnAccept(Session) error
	OnMessage(Session, []byte) ([]byte, error)
	OnClose(Session)
}

// LifecyclePlugin is an optional extension for plugins that run background
// goroutines or hold state that must be started and stopped together with the
// Gateway. Plugins that implement it are started by the plugin runner (in
// priority order) before any server starts, and stopped (in reverse order)
// after all sessions have been closed during Gateway.Stop.
//
// Implementations must be safe under repeated calls: Stop without Start,
// double Stop, and Start after Stop must all be no-ops or restarts (the
// built-in plugins rely on the lifecycle helper in internal/plugin for this).
type LifecyclePlugin interface {
	Start() error
	Stop() error
}

// BasePlugin is a no-op plugin for embedding.
type BasePlugin struct{}

func (BasePlugin) Name() string                                  { return "base" }
func (BasePlugin) Priority() int                                 { return 1000 }
func (BasePlugin) OnAccept(Session) error                        { return nil }
func (BasePlugin) OnMessage(_ Session, b []byte) ([]byte, error) { return b, nil }
func (BasePlugin) OnClose(Session)                               {}
