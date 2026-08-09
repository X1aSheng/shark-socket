package lwm2m

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// tlvResolver builds a resolver mapping resource IDs to data types (the role
// the object model plays for real LwM2M devices).
func tlvResolver(types map[int]ResourceType) func(int) ResourceType {
	return func(id int) ResourceType {
		if t, ok := types[id]; ok {
			return t
		}
		return ResourceOpaque
	}
}

// TestTLVEncodeOMAWireFormat verifies the encoder emits OMA LwM2M TLV records:
// a "Resource with Value" type byte (TT=11) with the identifier-width and
// length-width flags packed as the spec requires.
func TestTLVEncodeOMAWireFormat(t *testing.T) {
	// 8-bit id (0x01), 8-bit length (5), value "hello".
	// Type byte = 11 0 00 001 = 0xC1; then id 0x01, len 0x05, "hello".
	data, err := EncodeTLV([]tlvEntry{{ResourceID: 1, Type: ResourceString, Value: []byte("hello")}})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0xC1, 0x01, 0x05, 'h', 'e', 'l', 'l', 'o'}
	if !bytes.Equal(data, want) {
		t.Fatalf("wire bytes = % x, want % x", data, want)
	}

	// 16-bit id (0x0100 = 256), 16-bit length (0x0100 = 256 value bytes).
	// Type byte = 11 0 01 010 = 0xCA.
	val := make([]byte, 256)
	big, err := EncodeTLV([]tlvEntry{{ResourceID: 256, Type: ResourceOpaque, Value: val}})
	if err != nil {
		t.Fatal(err)
	}
	if big[0] != 0xCA {
		t.Fatalf("type byte = %#x, want 0xCA (16-bit id + 16-bit length)", big[0])
	}
	if len(big) != 1+2+2+256 {
		t.Fatalf("encoded length = %d, want %d", len(big), 1+2+2+256)
	}
}

// TestTLVRoundTripTyped verifies typed values round-trip when the data types
// are resolved from the object model (as a real LwM2M device would).
func TestTLVRoundTripTyped(t *testing.T) {
	intBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(intBytes, 42)
	floatBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(floatBytes, math.Float64bits(3.14))
	objLinkBytes := []byte{0x00, 0x01, 0x00, 0x02}

	entries := []tlvEntry{
		{ResourceID: 0, Type: ResourceString, Value: []byte("hello")},
		{ResourceID: 1, Type: ResourceInteger, Value: intBytes},
		{ResourceID: 2, Type: ResourceFloat, Value: floatBytes},
		{ResourceID: 3, Type: ResourceBoolean, Value: []byte{1}},
		{ResourceID: 4, Type: ResourceObjLink, Value: objLinkBytes},
		{ResourceID: 5, Type: ResourceTime, Value: []byte{0x60, 0x00, 0x00, 0x00}},
	}
	data, err := EncodeTLV(entries)
	if err != nil {
		t.Fatalf("EncodeTLV: %v", err)
	}
	resolver := tlvResolver(map[int]ResourceType{
		0: ResourceString, 1: ResourceInteger, 2: ResourceFloat,
		3: ResourceBoolean, 4: ResourceObjLink, 5: ResourceTime,
	})
	results, err := DecodeTLVTyped(data, resolver)
	if err != nil {
		t.Fatalf("DecodeTLVTyped: %v", err)
	}
	if len(results) != 6 {
		t.Fatalf("want 6 results, got %d", len(results))
	}
	if v, ok := results[0].ResourceValue().(string); !ok || v != "hello" {
		t.Fatalf("string value = %v", results[0].ResourceValue())
	}
	if v, ok := results[1].ResourceValue().(int64); !ok || v != 42 {
		t.Fatalf("int value = %v", results[1].ResourceValue())
	}
	if v, ok := results[2].ResourceValue().(float64); !ok || v != 3.14 {
		t.Fatalf("float value = %v", results[2].ResourceValue())
	}
	if v, ok := results[3].ResourceValue().(bool); !ok || !v {
		t.Fatalf("bool value = %v", results[3].ResourceValue())
	}
	if v, ok := results[4].ResourceValue().([]byte); !ok || !bytes.Equal(v, objLinkBytes) {
		t.Fatalf("objlink value = %v", results[4].ResourceValue())
	}
	if v, ok := results[5].ResourceValue().(time.Time); !ok || v.Unix() != 1610612736 {
		t.Fatalf("time value = %v", results[5].ResourceValue())
	}
}

// TestTLVDecodeRawReturnsOpaque verifies DecodeTLV returns raw bytes without an
// object model: the wire format does not carry the data type.
func TestTLVDecodeRawReturnsOpaque(t *testing.T) {
	data, err := EncodeTLV([]tlvEntry{{ResourceID: 1, Type: ResourceInteger, Value: []byte{42}}})
	if err != nil {
		t.Fatal(err)
	}
	results, err := DecodeTLV(data)
	if err != nil {
		t.Fatalf("DecodeTLV: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Type != ResourceOpaque {
		t.Fatalf("Type = %d, want ResourceOpaque (no object model)", results[0].Type)
	}
	if v, ok := results[0].ResourceValue().([]byte); !ok || !bytes.Equal(v, []byte{42}) {
		t.Fatalf("raw value = %v", results[0].ResourceValue())
	}
}

// TestTLVDecodeTruncated verifies malformed/truncated records are rejected.
func TestTLVDecodeTruncated(t *testing.T) {
	// 0x80 has TT=10 (Multiple Resource), which this codec does not decode.
	if _, err := DecodeTLV([]byte{0x80}); err == nil {
		t.Fatal("expected error for unsupported type")
	}
	// 0xC1 claims an 8-bit length that is missing.
	if _, err := DecodeTLV([]byte{0xC1, 0x01}); err == nil {
		t.Fatal("expected error for truncated length")
	}
	// 16-bit length claims more bytes than present.
	if _, err := DecodeTLV([]byte{0xC9, 0x01, 0x05, 'h', 'e'}); err == nil {
		t.Fatal("expected error for value length exceeding data")
	}
}

// TestTLVIntegerSignExtension verifies that 1-7 byte integer values are
// decoded as two's-complement signed (negative values sign-extended), matching
// LwM2M semantics.
func TestTLVIntegerSignExtension(t *testing.T) {
	cases := []struct {
		value []byte
		want  int64
	}{
		{[]byte{0x01}, 1},
		{[]byte{0x7F}, 127},
		{[]byte{0xFF}, -1},
		{[]byte{0xFF, 0xFE}, -2},
		{[]byte{0x00, 0xFF}, 255},
		{[]byte{0xFF, 0xFF, 0xFF, 0xFF}, -1},
		{[]byte{0x00, 0x00, 0x00, 0x01}, 1},
		{[]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFE}, -2},
	}
	for _, tc := range cases {
		r := tlvResource{ResourceID: 1, Type: ResourceInteger, Value: tc.value}
		got, ok := r.ResourceValue().(int64)
		if !ok {
			t.Fatalf("value %v: got %T, want int64", tc.value, r.ResourceValue())
		}
		if got != tc.want {
			t.Fatalf("value %v = %d, want %d", tc.value, got, tc.want)
		}
	}
}

func TestOperationMaskAllows(t *testing.T) {
	mask := OpRead | OpWrite
	if !mask.Allows(OpRead) {
		t.Fatal("expected OpRead allowed")
	}
	if !mask.Allows(OpWrite) {
		t.Fatal("expected OpWrite allowed")
	}
	if mask.Allows(OpExecute) {
		t.Fatal("expected OpExecute not allowed")
	}
}

// TestResourceValueFiveByteInteger verifies a 5-byte integer is decoded from
// all five bytes instead of silently truncating to the first four.
func TestResourceValueFiveByteInteger(t *testing.T) {
	r := tlvResource{ResourceID: 1, Type: ResourceInteger, Value: []byte{0x01, 0x00, 0x00, 0x00, 0x00}}
	v, ok := r.ResourceValue().(int64)
	if !ok {
		t.Fatalf("Value type = %T, want int64", r.ResourceValue())
	}
	if v != 0x0100000000 {
		t.Fatalf("Value = %d, want %d", v, int64(0x0100000000))
	}
}

// TestResourceValueFloat32 verifies a 4-byte float32 is decoded instead of
// returning 0.0.
func TestResourceValueFloat32(t *testing.T) {
	bits := math.Float32bits(1.5)
	val := make([]byte, 4)
	binary.BigEndian.PutUint32(val, bits)
	r := tlvResource{ResourceID: 1, Type: ResourceFloat, Value: val}
	v, ok := r.ResourceValue().(float64)
	if !ok {
		t.Fatalf("Value type = %T, want float64", r.ResourceValue())
	}
	if v != 1.5 {
		t.Fatalf("Value = %v, want 1.5", v)
	}
}
