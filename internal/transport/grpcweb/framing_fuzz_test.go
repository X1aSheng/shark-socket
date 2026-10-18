package grpcweb

import (
	"bytes"
	"testing"
)

// FuzzParseDataFrames feeds arbitrary bodies to the gRPC-Web request parser.
//
// Unlike the TCP framers this is not a stream parser -- an HTTP request body
// arrives whole -- but the length fields are still attacker-written, so the
// interesting inputs are a length that overruns the body, an unknown flag, a
// header with no payload, and a body that is shorter than one header.
//
// Properties:
//
//  1. no panic, and no allocation driven by a length field alone (a 5-byte
//     body claiming 0xFFFFFFFF payload must be rejected, not allocated);
//  2. strict mode never returns more payload bytes than the body carried;
//  3. lenient mode never fails: it either parses or reports "not framed" and
//     hands the body back untouched, which is what makes a non-gRPC-Web client
//     on the same server still work.
func FuzzParseDataFrames(f *testing.F) {
	f.Add(appendDataFrame(nil, []byte("hello")))
	f.Add(append(appendDataFrame(nil, []byte("a")), appendDataFrame(nil, []byte("bb"))...))
	f.Add(append(appendDataFrame(nil, []byte("x")), appendTrailerFrame(nil, 0, "")...))
	f.Add([]byte{0x00, 0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{0x80, 0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{0x01, 0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{0x00, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{0x00, 0x00, 0x00, 0x00, 0x01})
	f.Add([]byte{0x00, 0x00, 0x00})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, body []byte) {
		payload, framed, err := parseRequestPayload(body, true)
		if err == nil && framed && len(payload) > len(body) {
			t.Fatalf("parsed %d payload bytes out of a %d-byte body", len(payload), len(body))
		}

		loose, looseFramed, looseErr := parseRequestPayload(body, false)
		if looseErr != nil {
			t.Fatalf("lenient mode returned an error: %v", looseErr)
		}
		if !looseFramed {
			if !bytes.Equal(loose, body) {
				t.Fatalf("lenient mode dropped bytes while reporting no frame")
			}
			if err == nil && framed {
				t.Fatalf("strict mode parsed a frame that lenient mode did not")
			}
			return
		}
		// Both modes framed the body, so they must agree on the payload.
		if err != nil {
			t.Fatalf("lenient mode framed a body strict mode rejected: %v", err)
		}
		if !bytes.Equal(loose, payload) {
			t.Fatalf("modes disagree: strict %q, lenient %q", payload, loose)
		}
	})
}

// FuzzDataFrameRoundTrip checks the encoder against the parser: whatever this
// package writes must read back byte for byte, including the empty payload,
// because appendDataFrame is what every response goes through.
func FuzzDataFrameRoundTrip(f *testing.F) {
	f.Add([]byte("payload"))
	f.Add([]byte{})
	f.Add(make([]byte, 4096))

	f.Fuzz(func(t *testing.T, payload []byte) {
		body := appendDataFrame(nil, payload)
		got, framed, err := parseRequestPayload(body, true)
		if err != nil {
			t.Fatalf("own frame rejected: %v", err)
		}
		if !framed {
			t.Fatalf("own frame not recognised as framed")
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("round trip changed the payload: %d bytes in, %d out", len(payload), len(got))
		}
	})
}
