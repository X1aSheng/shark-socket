package types

import "github.com/X1aSheng/shark-socket/internal/errs"

// Plugin defines the interface for intercepting session lifecycle events.
// Plugins are ordered by Priority (lower = earlier execution).
type Plugin interface {
	Name() string
	Priority() int
	OnAccept(sess RawSession) error
	OnMessage(sess RawSession, data []byte) ([]byte, error)
	OnClose(sess RawSession)
}

// BasePlugin provides empty implementations for all Plugin methods.
// Embed this struct and override only the methods you need.
type BasePlugin struct{}

func (BasePlugin) Name() string                                          { return "base" }
func (BasePlugin) Priority() int                                         { return 100 }
func (BasePlugin) OnAccept(RawSession) error                             { return nil }
func (BasePlugin) OnMessage(_ RawSession, data []byte) ([]byte, error)   { return data, nil }
func (BasePlugin) OnClose(RawSession)                                    {}

// IsPluginBlock checks if the error indicates a connection should be blocked.
func IsPluginBlock(err error) bool { return err == errs.ErrBlock }

// IsPluginSkip checks if the error indicates message interception.
func IsPluginSkip(err error) bool { return err == errs.ErrSkip }

// IsPluginDrop checks if the error indicates message should be dropped.
func IsPluginDrop(err error) bool { return err == errs.ErrDrop }
