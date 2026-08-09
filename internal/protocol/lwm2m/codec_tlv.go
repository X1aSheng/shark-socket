package lwm2m

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// OMA LwM2M TLV wire format (v1.0/v1.1). A record is:
//
//	[type byte][identifier][length][value]
//
// The type byte packs: bits 7-6 TT (00 Object Instance, 01 Resource Instance,
// 10 Multiple Resource, 11 Resource with Value), bit 5 reserved (0), bits 4-3
// II (identifier length: 00=8-bit, 01=16-bit, 10=32-bit), bits 2-0 LLL (length:
// 000=fixed by type, 001=8-bit, 010=16-bit, 011=24-bit, 100=32-bit).
//
// The data type of a value is resolved from the object model, NOT carried on
// the wire: EncodeTLV emits "Resource with Value" records with an explicit
// 8/16-bit length, and DecodeTLVTyped resolves each resource's data type via a
// caller-supplied resolver. DecodeTLV returns raw values (Type=ResourceOpaque)
// for callers that only need the bytes.
//
// tlvEntry is a single resource value to encode. Type is used only to interpret
// the value for callers; it is not encoded on the wire.
type tlvEntry struct {
	ResourceID int
	Type       ResourceType
	Value      []byte
}

// OMA TLV flag constants for "Resource with Value" records (TT=11).
const (
	tlvResourceWithValue = 0xC0 // TT=11; II and LLL flags are OR-ed in below
	tlvID8               = 0x00
	tlvID16              = 0x01 << 3
	tlvID32              = 0x02 << 3
	tlvLen8              = 0x01
	tlvLen16             = 0x02
)

// EncodeTLV encodes resource values into OMA LwM2M TLV ("Resource with Value"
// records with an explicit 8/16-bit length). The resource data type is not
// encoded (it is resolved from the object model), so this output is parseable
// by real LwM2M devices.
func EncodeTLV(entries []tlvEntry) ([]byte, error) {
	var buf []byte
	for _, e := range entries {
		if e.ResourceID < 0 || e.ResourceID > 0xFFFFFFFF {
			return nil, fmt.Errorf("resource id %d out of range", e.ResourceID)
		}
		if len(e.Value) > 0xFFFF {
			return nil, fmt.Errorf("resource %d value length %d out of range", e.ResourceID, len(e.Value))
		}
		var idBytes []byte
		var typeByte byte = tlvResourceWithValue
		switch {
		case e.ResourceID <= 0xFF:
			typeByte |= tlvID8
			idBytes = []byte{byte(e.ResourceID)}
		case e.ResourceID <= 0xFFFF:
			typeByte |= tlvID16
			idBytes = make([]byte, 2)
			binary.BigEndian.PutUint16(idBytes, uint16(e.ResourceID))
		default:
			typeByte |= tlvID32
			idBytes = make([]byte, 4)
			binary.BigEndian.PutUint32(idBytes, uint32(e.ResourceID))
		}
		var lenBytes []byte
		if len(e.Value) <= 0xFF {
			typeByte |= tlvLen8
			lenBytes = []byte{byte(len(e.Value))}
		} else {
			typeByte |= tlvLen16
			lenBytes = make([]byte, 2)
			binary.BigEndian.PutUint16(lenBytes, uint16(len(e.Value)))
		}
		buf = append(buf, typeByte)
		buf = append(buf, idBytes...)
		buf = append(buf, lenBytes...)
		buf = append(buf, e.Value...)
	}
	return buf, nil
}

// tlvResource is a decoded TLV resource value.
type tlvResource struct {
	ResourceID int
	Type       ResourceType
	Value      []byte
}

// DecodeTLV decodes OMA TLV into raw resource values (Type=ResourceOpaque).
// Use DecodeTLVTyped when the resource data types are known (object model).
func DecodeTLV(data []byte) ([]tlvResource, error) {
	return decodeTLV(data, nil)
}

// DecodeTLVTyped decodes OMA TLV and resolves each resource's data type through
// resolver (the object model), matching how real LwM2M devices interpret TLV.
func DecodeTLVTyped(data []byte, resolver func(resourceID int) ResourceType) ([]tlvResource, error) {
	if resolver == nil {
		return decodeTLV(data, nil)
	}
	return decodeTLV(data, resolver)
}

func decodeTLV(data []byte, resolver func(int) ResourceType) ([]tlvResource, error) {
	var results []tlvResource
	offset := 0
	for offset < len(data) {
		if offset >= len(data) {
			return nil, fmt.Errorf("tlv: truncated type byte at offset %d", offset)
		}
		typeByte := data[offset]
		offset++
		if tt := (typeByte >> 6) & 0x03; tt != 0x03 {
			return nil, fmt.Errorf("tlv: unsupported type %d at offset %d (only Resource with Value)", tt, offset-1)
		}
		var resourceID int
		switch (typeByte >> 3) & 0x03 {
		case 0:
			if offset+1 > len(data) {
				return nil, fmt.Errorf("tlv: truncated 8-bit identifier at offset %d", offset)
			}
			resourceID = int(data[offset])
			offset++
		case 1:
			if offset+2 > len(data) {
				return nil, fmt.Errorf("tlv: truncated 16-bit identifier at offset %d", offset)
			}
			resourceID = int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2
		case 2:
			if offset+4 > len(data) {
				return nil, fmt.Errorf("tlv: truncated 32-bit identifier at offset %d", offset)
			}
			resourceID = int(binary.BigEndian.Uint32(data[offset : offset+4]))
			offset += 4
		default:
			return nil, fmt.Errorf("tlv: reserved identifier length at offset %d", offset-1)
		}

		var length int
		switch typeByte & 0x07 {
		case 1:
			if offset+1 > len(data) {
				return nil, fmt.Errorf("tlv: truncated 8-bit length at offset %d", offset)
			}
			length = int(data[offset])
			offset++
		case 2:
			if offset+2 > len(data) {
				return nil, fmt.Errorf("tlv: truncated 16-bit length at offset %d", offset)
			}
			length = int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2
		case 3:
			if offset+3 > len(data) {
				return nil, fmt.Errorf("tlv: truncated 24-bit length at offset %d", offset)
			}
			length = int(data[offset])<<16 | int(data[offset+1])<<8 | int(data[offset+2])
			offset += 3
		case 4:
			if offset+4 > len(data) {
				return nil, fmt.Errorf("tlv: truncated 32-bit length at offset %d", offset)
			}
			length = int(binary.BigEndian.Uint32(data[offset : offset+4]))
			offset += 4
		case 0:
			// LLL=000 means the length is fixed by the resource's data type and
			// size, which only the object model can resolve (and a data type
			// alone does not fix the size, e.g. Integer may be 1/2/4/8 bytes).
			return nil, fmt.Errorf("tlv: fixed-length record (LLL=000) at offset %d requires the object model", offset-1)
		default:
			return nil, fmt.Errorf("tlv: reserved length type at offset %d", offset-1)
		}
		if offset+length > len(data) {
			return nil, fmt.Errorf("tlv: value length %d exceeds data at offset %d", length, offset)
		}
		value := make([]byte, length)
		copy(value, data[offset:offset+length])
		offset += length
		rtype := ResourceOpaque
		if resolver != nil {
			rtype = resolver(resourceID)
		}
		results = append(results, tlvResource{ResourceID: resourceID, Type: rtype, Value: value})
	}
	return results, nil
}

// ResourceValue returns the decoded resource value as the appropriate Go type.
func (r tlvResource) ResourceValue() interface{} {
	switch r.Type {
	case ResourceString:
		return string(r.Value)
	case ResourceInteger:
		if len(r.Value) >= 8 {
			return int64(binary.BigEndian.Uint64(r.Value))
		}
		// 1-7 byte integers: build from every byte so high bytes are not
		// silently truncated (previously 5-7 byte values used only the
		// first 4 bytes).
		v := int64(0)
		for _, b := range r.Value {
			v = (v << 8) | int64(b)
		}
		// LwM2M integers are two's-complement signed; sign-extend values whose
		// most-significant byte has the high bit set (the 8-byte branch above
		// already sign-corrects via int64 conversion).
		if len(r.Value) > 0 && len(r.Value) < 8 && r.Value[0]&0x80 != 0 {
			v |= -1 << (8 * len(r.Value))
		}
		return v
	case ResourceFloat:
		if len(r.Value) >= 8 {
			bits := binary.BigEndian.Uint64(r.Value)
			return math.Float64frombits(bits)
		}
		if len(r.Value) == 4 {
			// 4-byte float32 value (previously decoded as 0.0).
			bits := binary.BigEndian.Uint32(r.Value)
			return float64(math.Float32frombits(bits))
		}
		return float64(0)
	case ResourceBoolean:
		return len(r.Value) > 0 && r.Value[0] != 0
	case ResourceTime:
		if len(r.Value) >= 4 {
			unix := int64(binary.BigEndian.Uint32(r.Value))
			return time.Unix(unix, 0)
		}
		return time.Time{}
	default:
		return r.Value
	}
}
