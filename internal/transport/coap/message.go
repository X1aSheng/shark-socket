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

	CodeEmpty               byte = 0
	CodeGet                 byte = 1
	CodePost                byte = 2
	CodePut                 byte = 3
	CodeDelete              byte = 4
	CodeCreated             byte = 65
	CodeDeleted             byte = 66
	CodeValid               byte = 67
	CodeChanged             byte = 68
	CodeContent             byte = 69
	CodeBadRequest          byte = 128
	CodeInternalServerError byte = 160

	ObserveOption uint16 = 6
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
	Options   map[uint16][]byte
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
	msg.Options, msg.Payload = parseOptions(rest)
	return msg, nil
}

func parseOptions(data []byte) (map[uint16][]byte, []byte) {
	options := make(map[uint16][]byte)
	offset := 0
	var prevNum uint16
	for offset < len(data) {
		if data[offset] == 0xff {
			payload := make([]byte, len(data)-offset-1)
			copy(payload, data[offset+1:])
			return options, payload
		}
		if offset+1 > len(data) {
			break
		}
		delta, length, advance := decodeOptionHeader(data[offset:])
		if advance == 0 {
			break
		}
		offset += advance
		deltaExtended, deltaBytes := readOptionExtended(data[offset:], delta)
		offset += deltaBytes
		lengthExtended, lengthBytes := readOptionExtended(data[offset:], length)
		offset += lengthBytes
		optionNum := prevNum + uint16(deltaExtended)
		prevNum = optionNum
		if offset+int(lengthExtended) > len(data) {
			break
		}
		val := make([]byte, lengthExtended)
		copy(val, data[offset:offset+int(lengthExtended)])
		options[optionNum] = val
		offset += int(lengthExtended)
	}
	return options, nil
}

func decodeOptionHeader(b []byte) (delta uint32, length uint32, advance int) {
	if len(b) == 0 {
		return 0, 0, 0
	}
	d := uint32(b[0] >> 4)
	l := uint32(b[0] & 0x0f)
	if d < 13 && l < 13 {
		return d, l, 1
	}
	return d, l, 1
}

func readOptionExtended(data []byte, nibble uint32) (uint32, int) {
	if nibble < 13 {
		return nibble, 0
	}
	if nibble == 13 {
		if len(data) < 1 {
			return 0, 0
		}
		return uint32(data[0]) + 13, 1
	}
	if nibble == 14 {
		if len(data) < 2 {
			return 0, 0
		}
		return uint32(binary.BigEndian.Uint16(data)) + 269, 2
	}
	return 0, 0
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
	if len(m.Options) > 0 {
		sorted := sortedOptionNums(m.Options)
		var prevNum uint16
		for _, num := range sorted {
			delta := num - prevNum
			prevNum = num
			val := m.Options[num]
			enc, err := encodeOption(delta, val)
			if err != nil {
				return nil, err
			}
			out = append(out, enc...)
		}
	}
	if len(m.Payload) > 0 {
		out = append(out, 0xff)
		out = append(out, m.Payload...)
	}
	return out, nil
}

func sortedOptionNums(opts map[uint16][]byte) []uint16 {
	nums := make([]uint16, 0, len(opts))
	for n := range opts {
		nums = append(nums, n)
	}
	for i := 0; i < len(nums)-1; i++ {
		for j := i + 1; j < len(nums); j++ {
			if nums[i] > nums[j] {
				nums[i], nums[j] = nums[j], nums[i]
			}
		}
	}
	return nums
}

func encodeOption(delta uint16, value []byte) ([]byte, error) {
	header, err := encodeOptionHeader(delta, uint16(len(value)))
	if err != nil {
		return nil, err
	}
	var buf []byte
	buf = append(buf, header...)
	buf = append(buf, value...)
	return buf, nil
}

func writeOptionExtended(buf []byte, v, base uint32) ([]byte, error) {
	if v < base {
		return buf, nil
	}
	if v < base+256 {
		return append(buf, byte(v-base)), nil
	}
	if v < base+65536 {
		ext := make([]byte, 2)
		binary.BigEndian.PutUint16(ext, uint16(v-base))
		return append(buf, ext...), nil
	}
	return buf, fmt.Errorf("%w: option extended value %d too large", ErrInvalidMessage, v)
}

func encodeOptionHeader(delta, length uint16) ([]byte, error) {
	var d, l byte
	switch {
	case delta < 13:
		d = byte(delta)
	case delta < 269:
		d = 13
	default:
		d = 14
	}
	switch {
	case length < 13:
		l = byte(length)
	case length < 269:
		l = 13
	default:
		l = 14
	}
	header := []byte{d<<4 | l}
	var err error
	header, err = writeOptionExtended(header, uint32(delta), 13)
	if err != nil {
		return nil, err
	}
	header, err = writeOptionExtended(header, uint32(length), 13)
	if err != nil {
		return nil, err
	}
	return header, nil
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
