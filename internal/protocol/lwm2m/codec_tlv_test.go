package lwm2m

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"
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

func TestTLVRoundTripObjLink(t *testing.T) {
	// ObjLink is two uint16 values packed into 4 bytes (big-endian).
	// ResourceValue() returns []byte for ObjLink type.
	objLinkBytes := []byte{0x00, 0x01, 0x00, 0x02}
	entries := []tlvEntry{
		{ResourceID: 3, Type: ResourceObjLink, Value: objLinkBytes},
	}
	data, err := EncodeTLV(entries)
	if err != nil {
		t.Fatalf("EncodeTLV: %v", err)
	}
	results, err := DecodeTLV(data)
	if err != nil {
		t.Fatalf("DecodeTLV: %v", err)
	}
	v, ok := results[0].ResourceValue().([]byte)
	if !ok {
		t.Fatalf("Value type = %T, want []byte", results[0].ResourceValue())
	}
	if !bytes.Equal(v, objLinkBytes) {
		t.Fatalf("Value = %v, want %v", v, objLinkBytes)
	}
	if results[0].Type != ResourceObjLink {
		t.Fatalf("Type = %d, want ResourceObjLink", results[0].Type)
	}
}

func TestTLVRoundTripTime(t *testing.T) {
	// Time returns time.Time from ResourceValue().
	entries := []tlvEntry{
		{ResourceID: 4, Type: ResourceTime, Value: []byte{0x60, 0x00, 0x00, 0x00}},
	}
	data, err := EncodeTLV(entries)
	if err != nil {
		t.Fatalf("EncodeTLV: %v", err)
	}
	results, err := DecodeTLV(data)
	if err != nil {
		t.Fatalf("DecodeTLV: %v", err)
	}
	v, ok := results[0].ResourceValue().(time.Time)
	if !ok {
		t.Fatalf("Value type = %T, want time.Time", results[0].ResourceValue())
	}
	// 0x60000000 = 1610612736 Unix timestamp
	if v.Unix() != 1610612736 {
		t.Fatalf("Unix = %d, want 1610612736", v.Unix())
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
