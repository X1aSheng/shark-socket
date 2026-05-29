package core

import "fmt"

// Protocol identifies the transport that owns a session.
type Protocol string

const (
	ProtocolTCP     Protocol = "tcp"
	ProtocolUDP     Protocol = "udp"
	ProtocolHTTP    Protocol = "http"
	ProtocolWS      Protocol = "websocket"
	ProtocolCoAP    Protocol = "coap"
	ProtocolQUIC    Protocol = "quic"
	ProtocolGRPCWeb Protocol = "grpc-web"
	ProtocolCustom  Protocol = "custom"
)

// SessionState is the runtime state of a session.
type SessionState uint8

const (
	StateConnecting SessionState = iota
	StateActive
	StateDraining
	StateClosed
)

func (s SessionState) String() string {
	switch s {
	case StateConnecting:
		return "connecting"
	case StateActive:
		return "active"
	case StateDraining:
		return "draining"
	case StateClosed:
		return "closed"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}
