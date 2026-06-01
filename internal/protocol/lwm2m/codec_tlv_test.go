package lwm2m

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestTLVRoundTripString(t *testing.T) {
	entries := []tlvEntry{
		{ResourceID: 0, Type: ResourceString, Value: []byte("hello")},
	}
	data, err := EncodeTLV(entries)
	if err != nil {
		t.Fatalf("EncodeTLV: %v", err)
	}
	results, err := DecodeTLV(data)
	if err != nil {
		t.Fatalf("DecodeTLV: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].ResourceID != 0 {
		t.Fatalf("ResourceID = %d, want 0", results[0].ResourceID)
	}
	if results[0].Type != ResourceString {
		t.Fatalf("Type = %d, want ResourceString", results[0].Type)
	}
	if v, ok := results[0].ResourceValue().(string); !ok || v != "hello" {
		t.Fatalf("Value = %v, want hello", results[0].ResourceValue())
	}
}

func TestTLVRoundTripInteger(t *testing.T) {
	val := make([]byte, 4)
	binary.BigEndian.PutUint32(val, 42)
	entries := []tlvEntry{
		{ResourceID: 1, Type: ResourceInteger, Value: val},
	}
	data, err := EncodeTLV(entries)
	if err != nil {
		t.Fatalf("EncodeTLV: %v", err)
	}
	results, err := DecodeTLV(data)
	if err != nil {
		t.Fatalf("DecodeTLV: %v", err)
	}
	if v, ok := results[0].ResourceValue().(int64); !ok || v != 42 {
		t.Fatalf("Value = %v, want 42", results[0].ResourceValue())
	}
}

func TestTLVRoundTripFloat(t *testing.T) {
	bits := math.Float64bits(3.14)
	val := make([]byte, 8)
	binary.BigEndian.PutUint64(val, bits)
	entries := []tlvEntry{
		{ResourceID: 2, Type: ResourceFloat, Value: val},
	}
	data, _ := EncodeTLV(entries)
	results, _ := DecodeTLV(data)
	if v, ok := results[0].ResourceValue().(float64); !ok || v != 3.14 {
		t.Fatalf("Value = %v, want 3.14", results[0].ResourceValue())
	}
}

func TestTLVRoundTripBoolean(t *testing.T) {
	entries := []tlvEntry{
		{ResourceID: 0, Type: ResourceBoolean, Value: []byte{1}},
	}
	data, _ := EncodeTLV(entries)
	results, _ := DecodeTLV(data)
	if v, ok := results[0].ResourceValue().(bool); !ok || !v {
		t.Fatalf("Value = %v, want true", results[0].ResourceValue())
	}
}

func TestTLVMultipleResources(t *testing.T) {
	entries := []tlvEntry{
		{ResourceID: 0, Type: ResourceString, Value: []byte("abc")},
		{ResourceID: 1, Type: ResourceBoolean, Value: []byte{0}},
		{ResourceID: 2, Type: ResourceOpaque, Value: []byte{0x01, 0x02, 0x03}},
	}
	data, err := EncodeTLV(entries)
	if err != nil {
		t.Fatalf("EncodeTLV: %v", err)
	}
	results, err := DecodeTLV(data)
	if err != nil {
		t.Fatalf("DecodeTLV: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
}

func TestTLVDecodeTruncated(t *testing.T) {
	_, err := DecodeTLV([]byte{0x80})
	if err == nil {
		t.Fatal("expected error for truncated data")
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
