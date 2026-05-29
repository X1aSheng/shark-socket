package coap

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	Version byte = 1

	TypeCON byte = 0
	TypeNON byte = 1
	TypeACK byte = 2
	TypeRST byte = 3

	CodeEmpty   byte = 0
	CodeGet     byte = 1
	CodePost    byte = 2
	CodePut     byte = 3
	CodeDelete  byte = 4
	CodeCreated byte = 65
	CodeDeleted byte = 66
	CodeValid   byte = 67
	CodeChanged byte = 68
	CodeContent byte = 69
)

var (
	ErrInvalidMessage = errors.New("invalid coap message")
	ErrInvalidVersion = errors.New("invalid coap version")
	ErrTokenTooLong   = errors.New("coap token too long")
)

type Message struct {
	Type      byte
	Code      byte
	MessageID uint16
	Token     []byte
	Payload   []byte
}

func Parse(data []byte) (Message, error) {
	if len(data) < 4 {
		return Message{}, ErrInvalidMessage
	}
	first := data[0]
	if first>>6 != Version {
		return Message{}, ErrInvalidVersion
	}
	tokenLen := int(first & 0x0f)
	if tokenLen > 8 {
		return Message{}, ErrTokenTooLong
	}
	if len(data) < 4+tokenLen {
		return Message{}, ErrInvalidMessage
	}
	msg := Message{
		Type:      (first >> 4) & 0x03,
		Code:      data[1],
		MessageID: binary.BigEndian.Uint16(data[2:4]),
		Token:     append([]byte(nil), data[4:4+tokenLen]...),
	}
	rest := data[4+tokenLen:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == 0xff {
			msg.Payload = append([]byte(nil), rest[i+1:]...)
			return msg, nil
		}
	}
	return msg, nil
}

func (m Message) Marshal() ([]byte, error) {
	if len(m.Token) > 8 {
		return nil, ErrTokenTooLong
	}
	if m.Type > TypeRST {
		return nil, fmt.Errorf("%w: type %d", ErrInvalidMessage, m.Type)
	}
	first := Version<<6 | (m.Type&0x03)<<4 | byte(len(m.Token))
	out := []byte{first, m.Code, 0, 0}
	binary.BigEndian.PutUint16(out[2:4], m.MessageID)
	out = append(out, m.Token...)
	if len(m.Payload) > 0 {
		out = append(out, 0xff)
		out = append(out, m.Payload...)
	}
	return out, nil
}

func ACK(req Message, code byte, payload []byte) Message {
	return Message{
		Type:      TypeACK,
		Code:      code,
		MessageID: req.MessageID,
		Token:     append([]byte(nil), req.Token...),
		Payload:   payload,
	}
}
