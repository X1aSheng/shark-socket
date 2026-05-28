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

// BasePlugin is a no-op plugin for embedding.
type BasePlugin struct{}

func (BasePlugin) Name() string                                  { return "base" }
func (BasePlugin) Priority() int                                 { return 1000 }
func (BasePlugin) OnAccept(Session) error                        { return nil }
func (BasePlugin) OnMessage(_ Session, b []byte) ([]byte, error) { return b, nil }
func (BasePlugin) OnClose(Session)                               {}
