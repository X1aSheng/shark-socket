package types

import (
	"context"
	"iter"
	"net"
	"time"
)

// Session is the generic session interface providing both universal Send([]byte)
// and type-safe SendTyped(msg M) paths.
type Session[M MessageConstraint] interface {
	// Identity (immutable).
	ID() uint64
	Protocol() ProtocolType
	RemoteAddr() net.Addr
	LocalAddr() net.Addr
	CreatedAt() time.Time

	// State (atomic operations).
	State() SessionState
	IsAlive() bool
	LastActiveAt() time.Time

	// Core send paths — both share the underlying writeQueue.
	Send(data []byte) error
	SendTyped(msg M) error

	// Lifecycle.
	Close() error
	Context() context.Context

	// Thread-safe metadata KV store.
	SetMeta(key string, val any)
	GetMeta(key string) (any, bool)
	DelMeta(key string)
}

// RawSession is the most common type alias, equivalent to a non-generic session.
type RawSession = Session[[]byte]

// SessionManager manages sessions across all protocols with sharded locking.
type SessionManager interface {
	Register(sess RawSession) error
	Unregister(id uint64)
	Get(id uint64) (RawSession, bool)
	Count() int64
	All() iter.Seq[RawSession]
	Range(fn func(RawSession) bool)
	Broadcast(data []byte)
	Close() error
}
