package api

import "github.com/X1aSheng/shark-socket/internal/core"

// Error sentinels an embedder can observe: returned by a call you make.
//
// The selection rule is narrow on purpose: a sentinel is exported here only if
// an embedder can observe it (returned by a call they make) or must produce it
// (returned from their Handler or Plugin for the chain to interpret). Anything
// else stays internal -- a sentinel that is exported but never returned is a
// contract pointing at nothing, and the next person writes errors.Is against it
// and waits for a branch that cannot happen.
//
// These are the framework's own values, not copies, so errors.Is matches the
// error that was actually returned without any unwrapping on the caller side.
// The full list, including the ones that are not reachable from here, is in
// docs/architecture/ERRORS.md.
var (
	// ErrClosed means the resource is shutting down. It is the generic form;
	// ErrSessionClosed and ErrServerClosed carry the same signal with a
	// narrower subject.
	ErrClosed = core.ErrClosed
	// ErrSessionClosed is returned by Session.Send after the session closed.
	// A send racing a peer disconnect normally surfaces this one.
	ErrSessionClosed = core.ErrSessionClosed
	// ErrServerClosed is returned by server operations after Stop began.
	ErrServerClosed = core.ErrServerClosed
	// ErrWriteQueueFull means the per-session write queue rejected the frame.
	// The frame was not sent; whether to retry is the caller's policy. Only
	// transports that queue writes (TCP) return it -- UDP, WebSocket and HTTP
	// write synchronously.
	ErrWriteQueueFull = core.ErrWriteQueueFull
	// ErrFrameTooLarge means the payload exceeded the transport's frame limit.
	// Permanent: the same payload will be rejected again.
	ErrFrameTooLarge = core.ErrFrameTooLarge
	// ErrSessionCapacity means the shared session index is full and Register
	// refused. On the accept path this is what MaxConnections produces.
	ErrSessionCapacity = core.ErrSessionCapacity
	// ErrDuplicateSession is returned by Register for an ID already present.
	ErrDuplicateSession = core.ErrDuplicateSession
	// ErrDuplicateProtocol is returned when two servers claim one Protocol.
	ErrDuplicateProtocol = core.ErrDuplicateProtocol
	// ErrInvalidArgument covers argument validation that failed before any I/O.
	ErrInvalidArgument = core.ErrInvalidArgument
	// ErrNoServers is returned by Gateway.Start with an empty registry.
	ErrNoServers = core.ErrNoServers
	// ErrPluginPanic means a plugin panicked and the panic was contained
	// (ADR-004). The gateway keeps serving; the affected operation failed.
	ErrPluginPanic = core.ErrPluginPanic
	// ErrHandlerPanic is the same containment for a user Handler.
	ErrHandlerPanic = core.ErrHandlerPanic
)

// Error sentinels an embedder must produce: returned from your Handler or
// Plugin for the chain to interpret. They are not failures; they are the only
// two control signals the framework accepts from user code.
var (
	// ErrPluginDrop, from OnMessage, discards this message and keeps the
	// session open. This is the only way to answer nothing without the
	// transport closing the connection on the error. Any other error from
	// OnMessage closes the session, which is the right default for a failure
	// but the wrong one for a rate limiter.
	ErrPluginDrop = core.ErrPluginDrop
	// ErrPluginBlock, from OnAccept, rejects the connection: the handshake is
	// refused and OnClose runs for the plugins that already accepted it.
	// Returning it from OnMessage closes the session instead.
	ErrPluginBlock = core.ErrPluginBlock
)
