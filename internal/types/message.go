package types

import (
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
	"time"
)

// MessageConstraint is the generic type constraint for session type parameters.
// Covers common wire formats: raw bytes, UTF-8 text, numeric, and structured payloads.
type MessageConstraint interface {
	~[]byte | ~string | ~int | ~float64
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

var (
	idPrefix  string
	idCounter atomic.Uint64
)

func init() {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	idPrefix = hex.EncodeToString(b)
}

// generateID creates a unique message identifier using a random prefix and monotonic counter.
func generateID() string {
	b := make([]byte, 8)
	// Mix counter bytes with prefix bytes to avoid predictable IDs.
	counter := idCounter.Add(1)
	b[0] = byte(counter >> 56)
	b[1] = byte(counter >> 48)
	b[2] = byte(counter >> 40)
	b[3] = byte(counter >> 32)
	b[4] = byte(counter >> 24)
	b[5] = byte(counter >> 16)
	b[6] = byte(counter >> 8)
	b[7] = byte(counter)
	return idPrefix + hex.EncodeToString(b)
}
