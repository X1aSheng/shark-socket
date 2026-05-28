package core

// Message is the canonical packet handed to handlers.
type Message struct {
	SessionID uint64
	Protocol  Protocol
	Payload   []byte
	Meta      map[string]string
}

// Handler processes decoded transport payloads.
type Handler func(Session, Message) error

// Codec adapts business message types to the raw transport layer.
type Codec[M any] interface {
	Encode(M) ([]byte, error)
	Decode([]byte) (M, error)
}

// TypedHandler processes business-level messages.
type TypedHandler[M any] func(Session, M) error

// AdaptTyped converts a typed handler into a raw handler.
func AdaptTyped[M any](codec Codec[M], h TypedHandler[M]) Handler {
	return func(sess Session, msg Message) error {
		decoded, err := codec.Decode(msg.Payload)
		if err != nil {
			return err
		}
		return h(sess, decoded)
	}
}
