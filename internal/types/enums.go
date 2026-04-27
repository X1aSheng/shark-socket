package types

import "unique"

// ProtocolType identifies the network protocol.
type ProtocolType uint8

const (
	TCP       ProtocolType = 1
	TLS       ProtocolType = 2
	UDP       ProtocolType = 3
	HTTP      ProtocolType = 4
	WebSocket ProtocolType = 5
	CoAP      ProtocolType = 6
	QUIC      ProtocolType = 7
	Custom    ProtocolType = 99
	Unknown   ProtocolType = 0
)

func (p ProtocolType) String() string {
	switch p {
	case TCP:
		return "TCP"
	case TLS:
		return "TLS"
	case UDP:
		return "UDP"
	case HTTP:
		return "HTTP"
	case WebSocket:
		return "WebSocket"
	case CoAP:
		return "CoAP"
	case QUIC:
		return "QUIC"
	case Custom:
		return "Custom"
	case Unknown:
		return "Unknown"
	default:
		return "Unknown"
	}
}

// ProtocolLabel returns a pooled string label for the protocol using Go 1.26 unique package.
func ProtocolLabel(p ProtocolType) string {
	return unique.Make(p.String()).Value()
}

// MessageType identifies the kind of message.
type MessageType uint8

const (
	Text      MessageType = 1
	Binary    MessageType = 2
	Ping      MessageType = 3
	Pong      MessageType = 4
	Close     MessageType = 5
	CoAPGet   MessageType = 10
	CoAPPost  MessageType = 11
	CoAPPut   MessageType = 12
	CoAPDel   MessageType = 13
	CoAPACK   MessageType = 14
)

func (m MessageType) String() string {
	switch m {
	case Text:
		return "Text"
	case Binary:
		return "Binary"
	case Ping:
		return "Ping"
	case Pong:
		return "Pong"
	case Close:
		return "Close"
	case CoAPGet:
		return "CoAPGet"
	case CoAPPost:
		return "CoAPPost"
	case CoAPPut:
		return "CoAPPut"
	case CoAPDel:
		return "CoAPDelete"
	case CoAPACK:
		return "CoAPACK"
	default:
		return "Unknown"
	}
}

// SessionState represents the lifecycle state of a session.
type SessionState uint8

const (
	Connecting SessionState = 0
	Active     SessionState = 1
	Closing    SessionState = 2
	Closed     SessionState = 3
)

func (s SessionState) String() string {
	switch s {
	case Connecting:
		return "Connecting"
	case Active:
		return "Active"
	case Closing:
		return "Closing"
	case Closed:
		return "Closed"
	default:
		return "Unknown"
	}
}
