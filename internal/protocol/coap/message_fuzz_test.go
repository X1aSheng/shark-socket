package coap

import (
	"bytes"
	"testing"
)

// FuzzParseMessage verifies ParseMessage doesn't panic on arbitrary input.
func FuzzParseMessage(f *testing.F) {
	// Seed corpus: valid CoAP messages and edge cases.
	// CON GET, empty token, no options, no payload
	f.Add([]byte{
		0x40,             // Ver=1, Type=CON, TKL=0
		byte(CodeGet),    // GET
		0x00, 0x01,       // Message ID = 1
	})

	// NON POST with token and payload
	f.Add([]byte{
		0x52,              // Ver=1, Type=NON, TKL=2
		byte(CodePost),    // POST
		0x00, 0x02,        // Message ID = 2
		0xAB, 0xCD,        // 2-byte token
		0xFF,              // Payload marker
		'h', 'e', 'l', 'l', 'o',
	})

	// CON GET with URI-Path option and payload
	f.Add([]byte{
		0x40,
		byte(CodeGet),
		0x00, 0x03,
		0xB1, 't', 'e', 's', 't', // Option delta=11, len=1, value="test"
		0xFF,
		'p', 'a', 'y', 'l', 'o', 'a', 'd',
	})

	// Edge cases
	f.Add([]byte{})                                                       // empty
	f.Add([]byte{0x00})                                                   // too short (1 byte)
	f.Add([]byte{0x00, 0x00, 0x00})                                      // too short (3 bytes)
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})                                // Ver=0 (invalid)
	f.Add([]byte{0x40, 0x00, 0x00, 0x00})                                // valid header only
	f.Add([]byte{0x4F, 0x00, 0x00, 0x01})                                // TKL=15 > 8 (invalid)
	f.Add([]byte{0xC0, 0x00, 0x00, 0x01})                                // Ver=3 (invalid)
	f.Add([]byte{0x41, 0x00, 0x00, 0x01, 0x00})                          // TKL=1, token present
	f.Add([]byte{0x41, 0x00, 0x00, 0x01})                                // TKL=1, token missing (truncated)
	f.Add([]byte{0x40, 0x00, 0x00, 0x01, 0xDD})                          // option extended delta (13)
	f.Add([]byte{0x40, 0x00, 0x00, 0x01, 0xDE, 0x00})                    // option extended delta (14, 2 bytes)
	f.Add([]byte{0x40, 0x00, 0x00, 0x01, 0xD1})                          // option extended len (13)
	f.Add([]byte{0x40, 0x00, 0x00, 0x01, 0xE1, 0x00})                    // option extended len (14, 2 bytes)
	f.Add([]byte{0x40, 0x00, 0x00, 0x01, 0x11, 0x00})                    // option delta=1, len=1, val=0
	f.Add([]byte{0x40, 0x00, 0x00, 0x01, 0xFF})                          // payload marker without payload
	f.Add([]byte{0x40, 0x00, 0x00, 0x01, 0xDD})                          // truncated extended delta

	// Valid roundtrip message (serialize then feed back as seed)
	msg := &CoAPMessage{
		Version:   1,
		Type:      CON,
		TokenLen:  2,
		Code:      CodeGet,
		MessageID: 42,
		Token:     []byte{0x12, 0x34},
		Options: []CoAPOption{
			{Delta: 11, Length: 4, Value: []byte("test")},
		},
		Payload: []byte("hello coap"),
	}
	if serialized, err := msg.Serialize(); err == nil {
		f.Add(serialized)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("ParseMessage panicked on %x: %v", data, r)
			}
		}()
		msg, err := ParseMessage(data)
		if err != nil {
			return // errors are expected for malformed input
		}
		// If parsing succeeded, serialization should roundtrip cleanly.
		if msg != nil {
			serialized, serr := msg.Serialize()
			if serr != nil {
				t.Logf("Serialize error for valid parse: %v", serr)
				return
			}
			// Re-parse the serialized output
			msg2, err2 := ParseMessage(serialized)
			if err2 != nil {
				t.Errorf("roundtrip re-parse failed: %v (input: %x, serialized: %x)", err2, data, serialized)
				return
			}
			if msg2 != nil && msg.MessageID != msg2.MessageID {
				t.Errorf("roundtrip MessageID mismatch: %d vs %d", msg.MessageID, msg2.MessageID)
			}
		}
	})
}

// FuzzCoAPRoundtrip verifies Serialize → ParseMessage roundtrip using
// arbitrary payload bytes fed through message construction.
func FuzzCoAPRoundtrip(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte{})
	f.Add(make([]byte, 100))

	f.Fuzz(func(t *testing.T, payload []byte) {
		msg := &CoAPMessage{
			Version:   1,
			Type:      CON,
			Code:      CodePost,
			MessageID: uint16(len(payload)),
			Payload:   payload,
		}

		serialized, err := msg.Serialize()
		if err != nil {
			return
		}

		msg2, err := ParseMessage(serialized)
		if err != nil {
			t.Errorf("roundtrip parse failed: %v", err)
			return
		}
		if msg2.MessageID != msg.MessageID {
			t.Errorf("MessageID mismatch: got %d, want %d", msg2.MessageID, msg.MessageID)
		}
		if !bytes.Equal(msg2.Payload, msg.Payload) {
			t.Errorf("payload mismatch")
		}
	})
}
