package shared

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
)

// chunkReader hands out at most step bytes per Read, so the buffered reader
// has to refill in the middle of signatures, address blocks and CRLF lines.
// Real balancers write the header in one segment; a hostile or merely unusual
// peer does not, and a parser that only works on one segment works by accident.
type chunkReader struct {
	data []byte
	step int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.step
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func parseProxyAt(data []byte, step int, mode ProxyProtocolMode) (proxyHeader, []byte, error) {
	br := bufio.NewReaderSize(&chunkReader{data: data, step: step}, proxyHeaderBuffer)
	hdr, err := readProxyHeader(br, mode)
	rest, _ := io.ReadAll(br)
	return hdr, rest, err
}

func proxyV2Header(cmd, famProto byte, payload []byte) []byte {
	out := make([]byte, 0, len(proxyV2Signature)+4+len(payload))
	out = append(out, proxyV2Signature[:]...)
	out = append(out, 0x20|cmd, famProto, byte(len(payload)>>8), byte(len(payload)))
	return append(out, payload...)
}

// expectedConsumption returns how many bytes a well-formed header at the start
// of data should consume, or -1 when data does not start with a signature this
// package recognises.
func expectedConsumption(data []byte) int {
	if len(data) >= len(proxyV2Signature) && bytes.Equal(data[:len(proxyV2Signature)], proxyV2Signature[:]) {
		if len(data) < 16 {
			return -1
		}
		length := int(binary.BigEndian.Uint16(data[14:16]))
		if length > proxyV2MaxPayload {
			return -1
		}
		return 16 + length
	}
	if bytes.HasPrefix(data, proxyV1Signature) {
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			return idx + 1
		}
	}
	return -1
}

func proxyAddrString(a net.Addr) string {
	if a == nil {
		return "<nil>"
	}
	return a.String()
}

// FuzzReadProxyHeader feeds arbitrary streams to the PROXY protocol parser.
//
// This is the one place in the project where a peer writes the address the
// rest of the stack then trusts: blacklists, per-IP quotas, audit logs and TLS
// session binding all key on it. So the properties are about what the parser
// must not do, not about what it accepts:
//
//  1. no panic, whatever the bytes;
//  2. the result must not depend on how the stream is chunked -- a header split
//     across reads is the same header;
//  3. a header that parses must consume exactly the header. The fuzz body is
//     the candidate header and a fixed marker is appended behind it: if the
//     leftover is anything but that marker, the parser ate payload, and a
//     framer three layers up would then report corruption it did not cause;
//  4. every rejection must carry an exported sentinel, because listeners
//     classify rejections by them and a bare error counts as "unknown";
//  5. mode Off reads nothing at all.
func FuzzReadProxyHeader(f *testing.F) {
	f.Add([]byte("PROXY TCP4 192.0.2.1 198.51.100.7 56324 443\r\n"))
	f.Add([]byte("PROXY TCP6 2001:db8::1 2001:db8::2 56324 443\r\n"))
	f.Add([]byte("PROXY UNKNOWN\r\n"))
	f.Add([]byte("PROXY UNKNOWN anything at all\r\n"))
	f.Add([]byte("PROXY TCP4 192.0.2.1 198.51.100.7 56324\r\n"))
	f.Add([]byte("PROXY TCP4 192.0.2.1 198.51.100.7 56324 443\n"))
	f.Add([]byte("PROXY TCP4 not-an-ip 198.51.100.7 1 2\r\n"))
	f.Add([]byte("PROXY SCTP4 192.0.2.1 198.51.100.7 1 2\r\n"))
	f.Add([]byte("PROXY "))
	f.Add([]byte{})
	f.Add(append([]byte{}, proxyV2Signature[:]...))
	f.Add(proxyV2Header(proxyV2CmdProxy, proxyFamTCP4,
		[]byte{192, 0, 2, 1, 198, 51, 100, 7, 0xDC, 0x04, 0x01, 0xBB}))
	f.Add(proxyV2Header(proxyV2CmdProxy, proxyFamTCP6, make([]byte, 36)))
	f.Add(proxyV2Header(proxyV2CmdLocal, 0x00, nil))
	f.Add(proxyV2Header(proxyV2CmdProxy, 0x00, nil))
	f.Add(proxyV2Header(proxyV2CmdProxy, proxyFamTCP4, []byte{1, 2, 3}))
	f.Add(proxyV2Header(proxyV2CmdProxy, proxyFamTCP4,
		append(make([]byte, 12), 0x03, 0x00, 0x04, 0xAA)))

	marker := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02}

	f.Fuzz(func(t *testing.T, header []byte) {
		data := make([]byte, 0, len(header)+len(marker))
		data = append(data, header...)
		data = append(data, marker...)

		for _, mode := range []ProxyProtocolMode{ProxyProtocolV1, ProxyProtocolV2, ProxyProtocolAuto} {
			_, rest, err := parseProxyAt(data, len(data)+1, mode)

			if err != nil {
				if !errors.Is(err, ErrProxyHeaderInvalid) &&
					!errors.Is(err, ErrProxyHeaderVersion) &&
					!errors.Is(err, ErrProxyHeaderTooLong) {
					t.Fatalf("mode %s: rejection is not an exported sentinel: %v", mode, err)
				}
			} else if want := expectedConsumption(data); want >= 0 && len(data)-len(rest) != want {
				// The header is self-describing: v1 ends at its CRLF, v2 says how
				// long it is. Consuming anything else means the parser either ate
				// payload or left header bytes on the stream.
				t.Fatalf("mode %s: consumed %d bytes, header says %d",
					mode, len(data)-len(rest), want)
			}

		}

		// Same bytes, different chunk boundaries: same answer. This runs once,
		// in Auto mode, because chunking is a property of the reader, not of the
		// configured version -- and because each extra parse costs coverage:
		// the first version of this target ran 15 parses per execution and
		// managed 142 execs/sec, which is a fuzz target that mostly fuzzes
		// itself.
		if len(data) <= 512 {
			whole, wholeRest, wholeErr := parseProxyAt(data, len(data)+1, ProxyProtocolAuto)
			for _, step := range []int{1, 3} {
				chunked, chunkedRest, chunkedErr := parseProxyAt(data, step, ProxyProtocolAuto)
				if (chunkedErr == nil) != (wholeErr == nil) {
					t.Fatalf("chunking at %d bytes changed the verdict: %v vs %v", step, chunkedErr, wholeErr)
				}
				if wholeErr != nil {
					continue
				}
				if proxyAddrString(chunked.source) != proxyAddrString(whole.source) ||
					proxyAddrString(chunked.dest) != proxyAddrString(whole.dest) {
					t.Fatalf("chunking at %d bytes changed the addresses", step)
				}
				if !bytes.Equal(chunkedRest, wholeRest) {
					t.Fatalf("chunking at %d bytes changed how much was consumed", step)
				}
			}
		}

		// Mode off means "this listener was not configured for PROXY protocol",
		// so it must not touch the stream at all.
		_, rest, err := parseProxyAt(data, len(data)+1, ProxyProtocolOff)
		if err == nil {
			t.Fatalf("mode off accepted a header")
		}
		if !bytes.Equal(rest, data) {
			t.Fatalf("mode off consumed %d bytes without parsing", len(data)-len(rest))
		}
	})
}
