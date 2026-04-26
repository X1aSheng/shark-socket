package types

import (
	"crypto/rand"
	"time"
)

// MessageConstraint is the generic type constraint for session type parameters.
// Any type can be used as a message payload, including []byte (RawSession).
type MessageConstraint interface {
	~[]byte | ~string | ~int | ~float64 | ~struct{} | any
}

// Message is a generic message envelope.
type Message[T any] struct {
	ID        string
	SessionID uint64
	Protocol  ProtocolType
	Type      MessageType
	Payload   T
	Timestamp time.Time
	Metadata  map[string]any
}

// RawMessage is the most common message type alias (90% of use cases).
type RawMessage = Message[[]byte]

// NewRawMessage creates a new RawMessage with a generated ID and pre-allocated metadata.
func NewRawMessage(sessionID uint64, proto ProtocolType, payload []byte) RawMessage {
	return RawMessage{
		ID:        generateID(),
		SessionID: sessionID,
		Protocol:  proto,
		Payload:   payload,
		Timestamp: time.Now(),
		Metadata:  make(map[string]any, 4),
	}
}

// generateID creates a unique message identifier using random bytes.
func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return string(b)
}
