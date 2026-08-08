package lwm2m

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// tlvEntry is a single resource value to encode.
type tlvEntry struct {
	ResourceID int
	Type       ResourceType
	Value      []byte
}

// EncodeTLV encodes resource values into binary TLV format.
// Format: [type(1B)][id(2B big-endian)][length(2B big-endian)][value]
func EncodeTLV(entries []tlvEntry) ([]byte, error) {
	var buf []byte
	for _, e := range entries {
		if e.ResourceID < 0 || e.ResourceID > 65535 {
			return nil, fmt.Errorf("resource id %d out of range", e.ResourceID)
		}
		if len(e.Value) > 65535 {
			return nil, fmt.Errorf("resource %d value length %d out of range", e.ResourceID, len(e.Value))
		}
		typeByte := resourceTypeToByte(e.Type)
		buf = append(buf, typeByte)
		idBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(idBytes, uint16(e.ResourceID))
		buf = append(buf, idBytes...)
		lenBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBytes, uint16(len(e.Value)))
		buf = append(buf, lenBytes...)
		buf = append(buf, e.Value...)
	}
	return buf, nil
}

func resourceTypeToByte(t ResourceType) byte {
	switch t {
	case ResourceString:
		return 0
	case ResourceInteger:
		return 1
	case ResourceFloat:
		return 2
	case ResourceBoolean:
		return 3
	case ResourceOpaque:
		return 4
	case ResourceObjLink:
		return 5
	case ResourceTime:
		return 6
	default:
		return 4
	}
}

func byteToResourceType(b byte) ResourceType {
	switch b {
	case 0:
		return ResourceString
	case 1:
		return ResourceInteger
	case 2:
		return ResourceFloat
	case 3:
		return ResourceBoolean
	case 4:
		return ResourceOpaque
	case 5:
		return ResourceObjLink
	case 6:
		return ResourceTime
	default:
		return ResourceOpaque
	}
}

// tlvResource is a decoded TLV resource value.
type tlvResource struct {
	ResourceID int
	Type       ResourceType
	Value      []byte
}

// DecodeTLV decodes binary TLV data into typed resource values.
func DecodeTLV(data []byte) ([]tlvResource, error) {
	var results []tlvResource
	offset := 0
	for offset < len(data) {
		if offset+5 > len(data) {
			return nil, fmt.Errorf("tlv: truncated record at offset %d", offset)
		}
		rtype := byteToResourceType(data[offset])
		offset++
		resourceID := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if offset+length > len(data) {
			return nil, fmt.Errorf("tlv: value length %d exceeds data at offset %d", length, offset)
		}
		value := make([]byte, length)
		copy(value, data[offset:offset+length])
		offset += length
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
