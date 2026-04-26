package types

import "context"

// MessageHandler is a function type that processes messages.
type MessageHandler[T any] func(sess Session[T], msg Message[T]) error

// RawHandler is the most common handler type alias.
type RawHandler = MessageHandler[[]byte]

// Server is the base interface for all protocol servers.
type Server interface {
	Start() error
	Stop(ctx context.Context) error
	Protocol() ProtocolType
}

// TypedServer is a generic server interface preserving compile-time type information.
type TypedServer[M MessageConstraint] interface {
	Start() error
	Stop(ctx context.Context) error
	Handler() MessageHandler[M]
}
