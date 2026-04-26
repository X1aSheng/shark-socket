package coap

import (
	"encoding/binary"
	"fmt"
)

// CoAP message types.
type CoAPMsgType uint8

const (
	CON CoAPMsgType = 0 // Confirmable
	NON CoAPMsgType = 1 // Non-Confirmable
	ACK CoAPMsgType = 2 // Acknowledgement
	RST CoAPMsgType = 3 // Reset
)

func (t CoAPMsgType) String() string {
	switch t {
	case CON:
		return "CON"
	case NON:
		return "NON"
	case ACK:
		return "ACK"
	case RST:
		return "RST"
	default:
		return "Unknown"
	}
}

// CoAPCode represents a CoAP method or response code.
type CoAPCode uint8

const (
	CodeEmpty    CoAPCode = 0
	CodeGet      CoAPCode = 1
	CodePost     CoAPCode = 2
	CodePut      CoAPCode = 3
	CodeDelete   CoAPCode = 4
	CodeCreated  CoAPCode = 0x41 // 2.01
	CodeDeleted  CoAPCode = 0x42 // 2.02
	CodeValid    CoAPCode = 0x43 // 2.03
	CodeChanged  CoAPCode = 0x44 // 2.04
	CodeContent  CoAPCode = 0x45 // 2.05
	CodeBadRequest   CoAPCode = 0x80 // 4.00
	CodeUnauthorized CoAPCode = 0x81 // 4.01
	CodeNotFound     CoAPCode = 0x84 // 4.04
	CodeMethodNotAllowed CoAPCode = 0x85 // 4.05
	CodeInternalError CoAPCode = 0xA0 // 5.00
)

// CoAPOption represents a CoAP option.
type CoAPOption struct {
	Delta  uint16
	Length uint16
	Value  []byte
}

// CoAPMessage is a decoded CoAP message.
type CoAPMessage struct {
	Version   uint8
	Type      CoAPMsgType
	TokenLen  uint8
	Code      CoAPCode
	MessageID uint16
	Token     []byte
	Options   []CoAPOption
	Payload   []byte
}

// ParseMessage parses a CoAP message from bytes.
func ParseMessage(data []byte) (*CoAPMessage, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("coap: message too short (%d bytes)", len(data))
	}

	msg := &CoAPMessage{}

	// First byte: Ver(2) | Type(2) | TKL(4)
	b0 := data[0]
	msg.Version = (b0 >> 6) & 0x03
	if msg.Version != 1 {
		return nil, fmt.Errorf("coap: invalid version %d", msg.Version)
	}
	msg.Type = CoAPMsgType((b0 >> 4) & 0x03)
	msg.TokenLen = b0 & 0x0F
	if msg.TokenLen > 8 {
		return nil, fmt.Errorf("coap: invalid token length %d", msg.TokenLen)
	}

	// Second byte: Code
	msg.Code = CoAPCode(data[1])

	// Bytes 3-4: Message ID (big endian)
	msg.MessageID = binary.BigEndian.Uint16(data[2:4])

	offset := 4

	// Token
	if msg.TokenLen > 0 {
		if offset+int(msg.TokenLen) > len(data) {
			return nil, fmt.Errorf("coap: token exceeds message length")
		}
		msg.Token = make([]byte, msg.TokenLen)
		copy(msg.Token, data[offset:offset+int(msg.TokenLen)])
		offset += int(msg.TokenLen)
	}

	// Options
	msg.Options = make([]CoAPOption, 0)
	for offset < len(data) {
		if data[offset] == 0xFF {
			offset++
			break // payload marker
		}

		optDelta := int(data[offset] >> 4)
		optLen := int(data[offset] & 0x0F)
		offset++

		// Extended delta
		if optDelta == 13 {
			if offset >= len(data) {
				return nil, fmt.Errorf("coap: extended delta truncated")
			}
			optDelta = int(data[offset]) + 13
			offset++
		} else if optDelta == 14 {
			if offset+1 >= len(data) {
				return nil, fmt.Errorf("coap: extended delta truncated")
			}
			optDelta = int(binary.BigEndian.Uint16(data[offset:offset+2])) + 269
			offset += 2
		}

		// Extended length
		if optLen == 13 {
			if offset >= len(data) {
				return nil, fmt.Errorf("coap: extended length truncated")
			}
			optLen = int(data[offset]) + 13
			offset++
		} else if optLen == 14 {
			if offset+1 >= len(data) {
				return nil, fmt.Errorf("coap: extended length truncated")
			}
			optLen = int(binary.BigEndian.Uint16(data[offset:offset+2])) + 269
			offset += 2
		}

		if offset+optLen > len(data) {
			return nil, fmt.Errorf("coap: option value exceeds message")
		}

		val := make([]byte, optLen)
		copy(val, data[offset:offset+optLen])
		msg.Options = append(msg.Options, CoAPOption{
			Delta: uint16(optDelta),
			Length: uint16(optLen),
			Value: val,
		})
		offset += optLen
	}

	// Payload
	if offset < len(data) {
		msg.Payload = make([]byte, len(data)-offset)
		copy(msg.Payload, data[offset:])
	}

	return msg, nil
}

// Serialize serializes a CoAP message to bytes.
func (m *CoAPMessage) Serialize() ([]byte, error) {
	size := 4 + int(m.TokenLen) + len(m.Payload)
	for _, opt := range m.Options {
		size += 1 + len(opt.Value)
	}
	if len(m.Payload) > 0 {
		size++ // payload marker
	}

	buf := make([]byte, 0, size)

	// Header byte
	b0 := (m.Version & 0x03) << 6
	b0 |= (uint8(m.Type) & 0x03) << 4
	b0 |= m.TokenLen & 0x0F
	buf = append(buf, b0)

	// Code
	buf = append(buf, uint8(m.Code))

	// Message ID
	buf = binary.BigEndian.AppendUint16(buf, m.MessageID)

	// Token
	buf = append(buf, m.Token...)

	// Options
	prevDelta := uint16(0)
	for _, opt := range m.Options {
		delta := opt.Delta - prevDelta
		buf = append(buf, encodeOptionHeader(delta, opt.Length))
		buf = append(buf, opt.Value...)
		prevDelta = opt.Delta
	}

	// Payload
	if len(m.Payload) > 0 {
		buf = append(buf, 0xFF)
		buf = append(buf, m.Payload...)
	}

	return buf, nil
}

func encodeOptionHeader(delta, length uint16) byte {
	d := byte(0)
	if delta < 13 {
		d = byte(delta)
	} else {
		d = 13
	}
	l := byte(0)
	if length < 13 {
		l = byte(length)
	} else {
		l = 13
	}
	return (d << 4) | l
}

// NewACK creates an ACK response for a CON message.
func NewACK(msg *CoAPMessage, code CoAPCode, payload []byte) *CoAPMessage {
	return &CoAPMessage{
		Version:   1,
		Type:      ACK,
		TokenLen:  uint8(len(msg.Token)),
		Code:      code,
		MessageID: msg.MessageID,
		Token:     msg.Token,
		Payload:   payload,
	}
}

// NewRST creates a RST message.
func NewRST(msgID uint16) *CoAPMessage {
	return &CoAPMessage{
		Version:   1,
		Type:      RST,
		Code:      CodeEmpty,
		MessageID: msgID,
	}
}
