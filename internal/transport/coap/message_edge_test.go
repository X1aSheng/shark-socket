package coap

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Test readOptionExtended edge cases
func TestReadOptionExtended(t *testing.T) {
	// nibble < 13: no extra bytes
	v, adv := readOptionExtended(nil, 5)
	if v != 5 || adv != 0 {
		t.Fatalf("nibble 5: got (%d,%d), want (5,0)", v, adv)
	}

	// nibble 13: 1 extra byte
	data := []byte{100}
	v, adv = readOptionExtended(data, 13)
	if v != 113 || adv != 1 { // 100 + 13
		t.Fatalf("nibble 13: got (%d,%d), want (113,1)", v, adv)
	}

	// nibble 13 with insufficient data
	v, adv = readOptionExtended([]byte{}, 13)
	if v != 0 || adv != 0 {
		t.Fatalf("nibble 13 no data: got (%d,%d), want (0,0)", v, adv)
	}

	// nibble 14: 2 extra bytes
	data = []byte{0, 100}
	v, adv = readOptionExtended(data, 14)
	expected := uint32(100) + 269
	if v != expected || adv != 2 {
		t.Fatalf("nibble 14: got (%d,%d), want (%d,2)", v, adv, expected)
	}

	// nibble 14 with insufficient data
	v, adv = readOptionExtended([]byte{1}, 14)
	if v != 0 || adv != 0 {
		t.Fatalf("nibble 14 short: got (%d,%d), want (0,0)", v, adv)
	}

	// nibble 15: invalid (returns 0,0)
	v, adv = readOptionExtended(nil, 15)
	if v != 0 || adv != 0 {
		t.Fatalf("nibble 15: got (%d,%d), want (0,0)", v, adv)
	}
}

// Test writeOptionExtended edge cases
func TestEncodeExtendedField(t *testing.T) {
	// Inline nibbles: no extension.
	buf, err := encodeExtendedField(nil, 5, 5)
	if err != nil || len(buf) != 0 {
		t.Fatalf("inline: err=%v len=%d", err, len(buf))
	}

	// Nibble 13: 1-byte value-13, range [13, 268].
	buf, err = encodeExtendedField(nil, 100, 13)
	if err != nil || len(buf) != 1 || buf[0] != 87 {
		t.Fatalf("nibble 13: got %v (want [87])", buf)
	}
	if _, err = encodeExtendedField(nil, 12, 13); err == nil {
		t.Fatal("nibble 13: expected range error for v=12")
	}
	if _, err = encodeExtendedField(nil, 269, 13); err == nil {
		t.Fatal("nibble 13: expected range error for v=269")
	}

	// Nibble 14: always 2 bytes value-269, range [269, 65535].
	buf, err = encodeExtendedField(nil, 269, 14)
	if err != nil || len(buf) != 2 || buf[0] != 0 || buf[1] != 0 {
		t.Fatalf("nibble 14 min: got %v", buf)
	}
	buf, err = encodeExtendedField(nil, 300, 14)
	if err != nil || len(buf) != 2 || buf[0] != 0 || buf[1] != 31 {
		t.Fatalf("nibble 14 (300): got %v (want [0 31])", buf)
	}
	buf, err = encodeExtendedField(nil, 525, 14)
	if err != nil || len(buf) != 2 {
		t.Fatalf("nibble 14 (525): err=%v len=%d", err, len(buf))
	}
	if _, err = encodeExtendedField(nil, 268, 14); err == nil {
		t.Fatal("nibble 14: expected range error for v=268")
	}
	if _, err = encodeExtendedField(nil, 65536, 14); err == nil {
		t.Fatal("nibble 14: expected range error for v=65536")
	}

	// Reserved nibble 15 must be rejected.
	if _, err = encodeExtendedField(nil, 100, 15); err == nil {
		t.Fatal("nibble 15: expected error")
	}
}

// Test encodeOptionHeader extended formats
func TestEncodeOptionHeader(t *testing.T) {
	// Small delta and length (< 13)
	hdr, err := encodeOptionHeader(5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hdr) != 1 || hdr[0] != 0x53 { // delta=5, len=3 -> 0x53
		t.Fatalf("small: got %02x", hdr)
	}

	// Medium delta (13-268), small length
	hdr, err = encodeOptionHeader(100, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hdr) != 2 {
		t.Fatalf("medium delta: expected 2 bytes, got %d", len(hdr))
	}

	// Large delta (>= 269), small length
	hdr, err = encodeOptionHeader(300, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hdr) != 3 {
		t.Fatalf("large delta: expected 3 bytes, got %d", len(hdr))
	}

	// Medium length (13-268), small delta
	hdr, err = encodeOptionHeader(3, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(hdr) != 2 {
		t.Fatalf("medium len: expected 2 bytes, got %d", len(hdr))
	}

	// Large length (>= 269)
	hdr, err = encodeOptionHeader(3, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(hdr) != 3 {
		t.Fatalf("large len: expected 3 bytes, got %d", len(hdr))
	}

	// Both large - 5 byte header (1 base + 2 delta ext + 2 len ext)
	hdr, err = encodeOptionHeader(300, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(hdr) != 5 {
		t.Fatalf("both large: expected 5 bytes, got %d", len(hdr))
	}
}

// Test decodeOptionHeader
func TestDecodeOptionHeaderEmpty(t *testing.T) {
	d, l, adv := decodeOptionHeader([]byte{})
	if d != 0 || l != 0 || adv != 0 {
		t.Fatalf("empty: got (%d,%d,%d), want (0,0,0)", d, l, adv)
	}
}

func TestDecodeOptionHeaderNormal(t *testing.T) {
	// Small delta and length
	d, l, adv := decodeOptionHeader([]byte{0x53})
	if d != 5 || l != 3 || adv != 1 {
		t.Fatalf("0x53: got (%d,%d,%d), want (5,3,1)", d, l, adv)
	}
}

// Test sortedOptionNums
func TestSortedOptionNums(t *testing.T) {
	opts := map[uint16][]byte{
		3:   []byte("c"),
		1:   []byte("a"),
		200: []byte("x"),
		10:  []byte("j"),
	}
	sorted := sortedOptionNums(opts)
	if len(sorted) != 4 {
		t.Fatalf("len = %d, want 4", len(sorted))
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1] > sorted[i] {
			t.Fatalf("not sorted: [%d]=%d > [%d]=%d", i-1, sorted[i-1], i, sorted[i])
		}
	}
}

// Test encodeOption with extended values
func TestEncodeOptionExtended(t *testing.T) {
	// Delta=300 needs 3-byte header
	enc, err := encodeOption(269, []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	if len(enc) < 3 {
		t.Fatalf("extended delta should have at least 3 header bytes")
	}
	// Verify roundtrip
	msg := Message{
		Type:      TypeCON,
		Code:      CodeContent,
		MessageID: 1,
		Payload:   []byte("data"),
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Type != TypeCON {
		t.Fatal("type mismatch")
	}
}

// Test Parse with options
func TestParseMessageWithOptions(t *testing.T) {
	msg := Message{
		Type:      TypeCON,
		Code:      CodeGet,
		MessageID: 42,
		Options: map[uint16][]byte{
			11: []byte("path"),
			12: []byte("ct"),
		},
		Payload: []byte("hello"),
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.MessageID != 42 {
		t.Fatalf("message ID = %d, want 42", parsed.MessageID)
	}
	if len(parsed.Options) != 2 {
		t.Fatalf("options = %d, want 2", len(parsed.Options))
	}
	if string(parsed.Payload) != "hello" {
		t.Fatalf("payload = %s, want hello", parsed.Payload)
	}
}

// Test response codes
func TestResponseCodes(t *testing.T) {
	// Verify all response codes are unique
	codes := map[string]byte{
		"CodeEmpty":               CodeEmpty,
		"CodeCreated":             CodeCreated,
		"CodeDeleted":             CodeDeleted,
		"CodeValid":               CodeValid,
		"CodeChanged":             CodeChanged,
		"CodeContent":             CodeContent,
		"CodeBadRequest":          CodeBadRequest,
		"CodeInternalServerError": CodeInternalServerError,
	}
	seen := make(map[byte]bool)
	for name, code := range codes {
		if seen[code] {
			t.Fatalf("duplicate code %d for %s", code, name)
		}
		seen[code] = true
	}
}

// Test Message with observe option
func TestMessageWithLargeObserveSeq(t *testing.T) {
	// Test that observe sequence is properly encoded with variable length
	for _, seq := range []uint32{0, 1, 255, 256, 65535, 65536} {
		encoded := encodeObserveSeq(seq)
		// Verify decode back
		var decoded uint32
		for _, b := range encoded {
			decoded = decoded<<8 | uint32(b)
		}
		if decoded != seq {
			t.Fatalf("encodeObserveSeq(%d) -> %v -> %d", seq, encoded, decoded)
		}
	}
}

// Test Marshal/Parse with large payload
func TestMessageMarshalLargePayload(t *testing.T) {
	for _, size := range []int{0, 1, 255, 1024} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i % 256)
		}
		msg := Message{
			Type:      TypeNON,
			Code:      CodeContent,
			MessageID: 1,
			Payload:   payload,
		}
		data, err := msg.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(parsed.Payload, payload) {
			t.Fatalf("size=%d: payload mismatch (%d vs %d)", size, len(parsed.Payload), len(payload))
		}
	}
}

// Test Marshal error cases
func TestMessageMarshalErrors(t *testing.T) {
	// Token too long
	msg := Message{Token: make([]byte, 9)}
	_, err := msg.Marshal()
	if err == nil {
		t.Fatal("expected error for token > 8")
	}

	// Invalid type
	msg = Message{Type: 5}
	_, err = msg.Marshal()
	if err == nil {
		t.Fatal("expected error for type > 3")
	}
}

// Fuzz target for message roundtrip
func FuzzMessageRoundTrip(f *testing.F) {
	f.Add([]byte{0x40, 1, 0, 1, 0xFF, 'h', 'e', 'l', 'l', 'o'})
	f.Add([]byte{0x40, 2, 0, 2, 0xFF})
	f.Add([]byte{0x50, 3, 0, 3, 0xd, 5, 'h', 'i', 0xFF, 'd', 'a', 't', 'a'})

	f.Fuzz(func(t *testing.T, data []byte) {
		msg, err := Parse(data)
		if err != nil {
			return // invalid input is OK
		}
		reencoded, err := msg.Marshal()
		if err != nil {
			// Some messages may not re-encode (e.g., if token is too long from parse)
			return
		}
		reparsed, err := Parse(reencoded)
		if err != nil {
			t.Fatalf("re-encoded message failed to parse: %v\noriginal: %v\nreencoded: %v",
				err, data, reencoded)
		}
		if msg.Type != reparsed.Type || msg.Code != reparsed.Code || msg.MessageID != reparsed.MessageID {
			t.Fatalf("roundtrip mismatch:\n  type: %d vs %d\n  code: %d vs %d\n  mid: %d vs %d",
				msg.Type, reparsed.Type, msg.Code, reparsed.Code, msg.MessageID, reparsed.MessageID)
		}
	})
}

// Test Marshal with multiple options having extended deltas
func TestMarshalManyOptions(t *testing.T) {
	options := map[uint16][]byte{}
	for _, num := range []uint16{1, 50, 300, 400, 500} {
		options[num] = []byte{byte(num), byte(num >> 8)}
	}
	msg := Message{
		Type:      TypeCON,
		Code:      CodeContent,
		MessageID: 100,
		Options:   options,
		Payload:   []byte("multi-option"),
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Options) != 5 {
		t.Fatalf("options count = %d, want 5", len(parsed.Options))
	}
}

// Test writeOptionExtended with base offset
// Verify the encoder and decoder agree on every delta in the nibble-13/14
// boundary region. This is the round-trip invariant that the option encoding
// bug violated (encoder wrote v-13 for nibble 14, decoder reads v-269).
func TestEncodeDecodeExtendedAgreement(t *testing.T) {
	for _, v := range []uint16{13, 100, 268, 269, 300, 524, 525, 526, 65535} {
		hdr, err := encodeOptionHeader(v, 0)
		if err != nil {
			t.Fatalf("delta %d encode: %v", v, err)
		}
		d, _, _ := decodeOptionHeader(hdr)
		dec, _ := readOptionExtended(hdr[1:], d)
		if dec != uint32(v) {
			t.Fatalf("delta %d: decoded %d", v, dec)
		}
	}
}

// Test encodeOptionHeader mid-range values
func TestEncodeOptionHeaderMidRange(t *testing.T) {
	// Delta=13 (boundary), len=0
	hdr, err := encodeOptionHeader(13, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hdr) != 2 {
		t.Fatalf("delta=13: expected 2 bytes, got %d", len(hdr))
	}

	// Delta=268 (boundary), len=0
	hdr, err = encodeOptionHeader(268, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hdr) != 2 {
		t.Fatalf("delta=268: expected 2 bytes, got %d", len(hdr))
	}

	// Delta=269 (boundary), len=0
	hdr, err = encodeOptionHeader(269, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hdr) != 3 {
		t.Fatalf("delta=269: expected 3 bytes, got %d", len(hdr))
	}
}

// Test message types
func TestMessageTypes(t *testing.T) {
	if TypeCON != 0 || TypeNON != 1 || TypeACK != 2 || TypeRST != 3 {
		t.Fatal("message type values incorrect")
	}
}

// Test Token handling in marshal/parse
func TestTokenRoundTrip(t *testing.T) {
	for _, tokenLen := range []int{0, 1, 4, 8} {
		token := make([]byte, tokenLen)
		for i := range token {
			token[i] = byte(i + 1)
		}
		msg := Message{
			Type:      TypeCON,
			Code:      CodeGet,
			MessageID: 1,
			Token:     token,
		}
		data, err := msg.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(parsed.Token, token) {
			t.Fatalf("token=%v, got %v", token, parsed.Token)
		}
	}
}

// Round-trip an option whose number (delta) is in the nibble-14 range (>= 269).
// Regression: encoder wrote v-13 for nibble=14 while the decoder reads v-269,
// corrupting any option number >= 269.
func TestOptionRoundTripLargeDelta(t *testing.T) {
	for _, num := range []uint16{269, 300, 525, 526, 1000, 65535} {
		msg := Message{
			Type:      TypeCON,
			Code:      CodeGet,
			MessageID: 1,
			Options:   map[uint16][]byte{num: {0x01}},
		}
		data, err := msg.Marshal()
		if err != nil {
			t.Fatalf("option %d marshal: %v", num, err)
		}
		parsed, err := Parse(data)
		if err != nil {
			t.Fatalf("option %d parse: %v", num, err)
		}
		got, ok := parsed.Options[num]
		if !ok {
			t.Fatalf("option %d missing after round-trip", num)
		}
		if !bytes.Equal(got, []byte{0x01}) {
			t.Fatalf("option %d value = %v, want [1]", num, got)
		}
	}
}

// Round-trip an option whose value length is in the nibble-14 range (>= 269).
// Regression: encoder wrote len-13 for nibble=14 while the decoder reads len-269.
func TestOptionRoundTripLargeLength(t *testing.T) {
	for _, size := range []int{269, 300, 525, 526, 2048} {
		value := make([]byte, size)
		for i := range value {
			value[i] = byte(i)
		}
		msg := Message{
			Type:      TypeCON,
			Code:      CodeGet,
			MessageID: 1,
			Options:   map[uint16][]byte{11: value},
		}
		data, err := msg.Marshal()
		if err != nil {
			t.Fatalf("size %d marshal: %v", size, err)
		}
		parsed, err := Parse(data)
		if err != nil {
			t.Fatalf("size %d parse: %v", size, err)
		}
		got, ok := parsed.Options[11]
		if !ok {
			t.Fatalf("option 11 missing after round-trip (size %d)", size)
		}
		if !bytes.Equal(got, value) {
			t.Fatalf("option 11 size %d: round-trip mismatch", size)
		}
	}
}

// Round-trip boundaries between nibble 13 and 14 for both delta and length.
func TestOptionRoundTripBoundaries(t *testing.T) {
	cases := []struct {
		num    uint16
		length int
	}{
		{12, 1},    // delta 12 inline
		{13, 1},    // delta 13 -> nibble 13
		{268, 1},   // delta 268 -> nibble 13 upper bound
		{1, 12},    // length 12 inline
		{1, 13},    // length 13 -> nibble 13
		{1, 268},   // length 268 -> nibble 13 upper bound
		{268, 268}, // both at nibble-13 upper bound
	}
	for _, c := range cases {
		value := make([]byte, c.length)
		msg := Message{
			Type:      TypeCON,
			Code:      CodeGet,
			MessageID: 1,
			Options:   map[uint16][]byte{c.num: value},
		}
		data, err := msg.Marshal()
		if err != nil {
			t.Fatalf("marshal %d/%d: %v", c.num, c.length, err)
		}
		parsed, err := Parse(data)
		if err != nil {
			t.Fatalf("parse %d/%d: %v", c.num, c.length, err)
		}
		got, ok := parsed.Options[c.num]
		if !ok {
			t.Fatalf("option %d missing (len %d)", c.num, c.length)
		}
		if len(got) != c.length {
			t.Fatalf("option %d len %d, want %d", c.num, len(got), c.length)
		}
	}
}

// Reserved nibble 15 must be rejected as malformed, not misparsed or spun.
func TestParseRejectsReservedNibble15(t *testing.T) {
	// 0xF0: delta nibble 15 (reserved), length 0.
	// parseOptions must terminate and must not produce a bogus option 0.
	opts, _ := parseOptions([]byte{0xF0})
	if _, ok := opts[0]; ok {
		t.Fatal("reserved nibble 15 produced an option entry")
	}
	// 0x1F: delta 1, length nibble 15 (reserved).
	opts, _ = parseOptions([]byte{0x1F, 0x00})
	if _, ok := opts[1]; ok {
		t.Fatal("reserved length nibble 15 produced an option entry")
	}
}

// Benchmark for encodeObserveSeq
func BenchmarkEncodeObserveSeq(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = encodeObserveSeq(uint32(i % 100000))
	}
}

// Benchmark for readOptionExtended
func BenchmarkReadOptionExtended(b *testing.B) {
	data := []byte{0, 100}
	for i := 0; i < b.N; i++ {
		_, _ = readOptionExtended(data, 14)
	}
}

// Benchmark for encodeExtendedField
func BenchmarkEncodeExtendedField(b *testing.B) {
	buf := make([]byte, 0, 8)
	for i := 0; i < b.N; i++ {
		buf, _ = encodeExtendedField(buf[:0], 1024, 13)
	}
}

// Benchmark for sortedOptionNums
func BenchmarkSortedOptionNums(b *testing.B) {
	opts := map[uint16][]byte{
		1: {}, 10: {}, 100: {}, 200: {}, 500: {},
	}
	for i := 0; i < b.N; i++ {
		_ = sortedOptionNums(opts)
	}
}

func init() {
	// Ensure binary import is used
	_ = binary.BigEndian
}
