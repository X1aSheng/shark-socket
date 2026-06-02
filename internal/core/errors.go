package core

import "errors"

var (
	ErrClosed             = errors.New("closed")
	ErrDuplicateProtocol  = errors.New("duplicate protocol")
	ErrDuplicateSession   = errors.New("duplicate session")
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrNoServers          = errors.New("no servers registered")
	ErrPluginPanic        = errors.New("plugin panicked")
	ErrSessionCapacity    = errors.New("session capacity reached")
	ErrSessionClosed      = errors.New("session closed")
	ErrServerClosed       = errors.New("server closed")
	ErrWriteQueueFull     = errors.New("write queue full")
	ErrFrameTooLarge      = errors.New("frame too large")
	ErrUnsupportedFeature = errors.New("unsupported feature")
)
