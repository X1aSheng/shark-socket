package core

import "context"

// Server is the minimal transport contract.
type Server interface {
	Protocol() Protocol
	Start(context.Context) error
	Stop(context.Context) error
}

// RuntimeConfigurable is implemented by transports that can be wired by Gateway.
type RuntimeConfigurable interface {
	UseRuntime(Runtime)
}

// Runtime is injected into transports by Gateway.
type Runtime interface {
	Sessions() SessionManager
	Plugins() PluginRunner
	Logger() Logger
	Metrics() Metrics
	Tracer() Tracer
}

// PluginRunner executes the resolved plugin chain.
type PluginRunner interface {
	OnAccept(Session) error
	OnMessage(Session, []byte) ([]byte, error)
	OnClose(Session)

	// StartAll starts every plugin that implements LifecyclePlugin, in
	// priority order, rolling back already-started plugins on failure.
	// Gateway.Start calls it before starting any server.
	StartAll() error

	// StopAll stops every plugin that implements LifecyclePlugin, in
	// reverse priority order. Gateway.Stop calls it after all sessions
	// have been closed. Repeated or unpaired calls are no-ops.
	StopAll()
}

// StagedServer is optional. Gateway uses it for precise graceful shutdown.
type StagedServer interface {
	StopAccept(context.Context) error
	Drain(context.Context) error
	CloseSessions(context.Context) error
}
