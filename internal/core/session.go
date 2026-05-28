package core

import (
	"context"
	"net"
	"time"
)

// Session is intentionally raw. Typed behavior is layered through Codec.
type Session interface {
	ID() uint64
	Protocol() Protocol
	RemoteAddr() net.Addr
	LocalAddr() net.Addr
	State() SessionState
	CreatedAt() time.Time
	LastActiveAt() time.Time
	Context() context.Context
	Send([]byte) error
	Close(context.Context) error
	SetMeta(string, any)
	GetMeta(string) (any, bool)
	DelMeta(string)
}

// SessionManager owns the global session index. It never closes injected
// resources implicitly; callers choose the lifecycle stage.
type SessionManager interface {
	NextID() uint64
	Register(Session) error
	Unregister(uint64)
	Get(uint64) (Session, bool)
	Count() int64
	Snapshot() []Session
	Range(func(Session) bool)
	Broadcast([]byte) error
	CloseAll(context.Context) error
}
