package coap

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// rfcValues are the option values from RFC 7959 section 2.3: the wire format is
// the NUM/M/SZX triple in the minimal number of bytes.
func TestBlockOptionEncoding(t *testing.T) {
	cases := []struct {
		name string
		opt  BlockOption
		wire []byte
	}{
		{"all zero is the empty value", BlockOption{Num: 0, More: false, SizeExp: 0}, nil},
		// RFC 7959 2.3: "0x0a" = NUM 0, M 1, SZX 2 (64 bytes)
		{"rfc example 0x0a", BlockOption{Num: 0, More: true, SizeExp: 2}, []byte{0x0a}},
		// RFC 7959 2.3: "0x2d" = NUM 2, M 1, SZX 5 (512 bytes)
		{"rfc example 0x2d", BlockOption{Num: 2, More: true, SizeExp: 5}, []byte{0x2d}},
		// NUM 16 needs three nibbles, so the value grows to two bytes.
		{"two bytes", BlockOption{Num: 16, More: false, SizeExp: 6}, []byte{0x01, 0x06}},
		// 4096<<4 = 0x10000, so three bytes even though the middle one is zero.
		{"three bytes", BlockOption{Num: 4096, More: true, SizeExp: 6}, []byte{0x01, 0x00, 0x0e}},
		{"largest NUM still in three bytes", BlockOption{Num: 65536, More: false, SizeExp: 6}, []byte{0x10, 0x00, 0x06}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.opt.Marshal()
			if !bytes.Equal(got, tc.wire) {
				t.Fatalf("Marshal = %x, want %x", got, tc.wire)
			}
			back, err := parseBlockOption(tc.wire)
			if err != nil {
				t.Fatalf("parse(%x): %v", tc.wire, err)
			}
			if back != tc.opt {
				t.Fatalf("round trip = %#v, want %#v", back, tc.opt)
			}
		})
	}
}

func TestBlockOptionRejectsIllegalValues(t *testing.T) {
	if _, err := parseBlockOption([]byte{1, 2, 3, 4}); err == nil {
		t.Fatal("a four-byte Block2 value was accepted; RFC 7959 allows at most three")
	}
	// SZX 7 is reserved: it would name a 2048-byte block that no MTU carries.
	if _, err := parseBlockOption([]byte{0x07}); err == nil {
		t.Fatal("the reserved SZX 7 was accepted")
	}
}

func TestSliceBlock(t *testing.T) {
	body := []byte("0123456789") // 10 bytes
	cases := []struct {
		num   uint32
		size  int
		chunk string
		more  bool
		ok    bool
	}{
		{0, 4, "0123", true, true},
		{1, 4, "4567", true, true},
		{2, 4, "89", false, true},          // partial last block, M cleared
		{3, 4, "", false, false},           // past the end: 4.02
		{20, 4, "", false, false},          // far past the end
		{0, 16, "0123456789", false, true}, // fits without a remainder
	}
	for _, tc := range cases {
		chunk, more, ok := sliceBlock(body, tc.num, tc.size)
		if ok != tc.ok || more != tc.more || string(chunk) != tc.chunk {
			t.Fatalf("sliceBlock(%d, %d) = %q, more=%v, ok=%v; want %q, more=%v, ok=%v",
				tc.num, tc.size, chunk, more, ok, tc.chunk, tc.more, tc.ok)
		}
	}
}

// TestSliceBlockRejectsOffsetAtEnd pins the boundary: a body of exactly two
// blocks ends at block 1, so block 2 names a byte that does not exist and must
// be refused rather than answered with an empty payload - an empty payload with
// M cleared would tell the client the resource is one block longer than it is.
func TestSliceBlockRejectsOffsetAtEnd(t *testing.T) {
	body := []byte("12345678") // 8 bytes, exactly two 4-byte blocks
	if _, more, ok := sliceBlock(body, 1, 4); !ok || more {
		t.Fatalf("block 1 = ok %v more %v, want the last block", ok, more)
	}
	if _, _, ok := sliceBlock(body, 2, 4); ok {
		t.Fatal("block 2 of an exactly-two-block body was accepted; its offset is past the end")
	}
}

func TestNegotiateBlock2UsesServerMaximum(t *testing.T) {
	req, err := negotiateBlock2(nil, 512)
	if err != nil {
		t.Fatal(err)
	}
	if req.present || req.size != 512 {
		t.Fatalf("absent option = %#v, want present=false size=512", req)
	}

	// The peer asks for 1024 (SZX 6), the server only offers 512: the offset is
	// computed against the requested size, then re-divided - block 2 of 1024 is
	// byte 2048, which is block 4 of 512.
	req, err = negotiateBlock2(map[uint16][]byte{Block2Option: {0x26}}, 512)
	if err != nil {
		t.Fatal(err)
	}
	if !req.present || req.offset != 2048 || req.size != 512 {
		t.Fatalf("negotiated = %#v, want offset 2048 size 512", req)
	}

	// A peer that asks for a smaller block keeps it: 256 bytes (SZX 4).
	req, err = negotiateBlock2(map[uint16][]byte{Block2Option: {0x04}}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if req.size != 256 || req.offset != 0 {
		t.Fatalf("smaller request = %#v, want size 256 offset 0", req)
	}
}

func TestBlockForLeavesSmallBodiesAlone(t *testing.T) {
	body := []byte("small")
	chunk, opt, ok := blockFor(body, blockRequest{size: 1024})
	if !ok || opt != nil || !bytes.Equal(chunk, body) {
		t.Fatalf("blockFor = %q, opt=%v, ok=%v; want the body unchanged with no option", chunk, opt, ok)
	}
	// Once the peer uses the option, the response carries it even for a body
	// that fits: the peer needs M=0 to know the transfer is over.
	chunk, opt, ok = blockFor(body, blockRequest{present: true, size: 1024})
	if !ok || opt == nil || opt.More || opt.Num != 0 {
		t.Fatalf("blockFor with the option = %q, opt=%#v, ok=%v", chunk, opt, ok)
	}
	if !bytes.Equal(chunk, body) {
		t.Fatalf("chunk = %q, want the whole body", chunk)
	}
}

// blockBody builds a deterministic body so a reassembly mismatch names the byte
// that diverged rather than just the length.
func blockBody(n int) []byte {
	body := make([]byte, n)
	for i := range body {
		body[i] = byte(i * 7)
	}
	return body
}

func startBlockServer(t *testing.T, bodySize int, opts ...Option) *Server {
	t.Helper()
	body := blockBody(bodySize)
	all := append([]Option{
		WithAddr("127.0.0.1:0"),
		WithResponder(func(core.Session, core.Message) ([]byte, error) {
			return body, nil
		}),
	}, opts...)
	server := NewServer(all...)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopGateway(t, gateway) })
	return server
}

// coapExchange sends one request and returns the parsed reply.
func coapExchange(t *testing.T, conn net.Conn, req Message) Message {
	t.Helper()
	data, err := req.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// Large enough for a 4 KiB body in one datagram (the block-wise-off case).
	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Parse(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func dialCoAP(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestCoAPBlock2Reassembly is C3: a 4 KiB resource is delivered in 1 KiB blocks
// and reassembles byte for byte. This is the shape Leshan and other standard
// clients use for any payload they consider large.
func TestCoAPBlock2Reassembly(t *testing.T) {
	const bodySize, blockSize = 4096, 1024
	server := startBlockServer(t, bodySize, WithMaxBlockSize(blockSize))
	conn := dialCoAP(t, server.Addr().String())

	var got []byte
	num := uint32(0)
	for blocks := 0; ; blocks++ {
		if blocks > 8 {
			t.Fatal("block-wise transfer did not terminate")
		}
		req := Message{Type: TypeCON, Code: CodeGet, MessageID: uint16(100 + blocks), Token: []byte{1}}
		if blocks > 0 {
			req.Options = map[uint16][]byte{
				Block2Option: BlockOption{Num: num, SizeExp: sizeExp(blockSize)}.Marshal(),
			}
		}
		resp := coapExchange(t, conn, req)
		if resp.Code != CodeContent {
			t.Fatalf("block %d: code = %d, want 2.05 Content", num, resp.Code)
		}
		opt, err := parseBlockOption(resp.Options[Block2Option])
		if err != nil {
			t.Fatalf("block %d: missing or invalid Block2 option: %v", num, err)
		}
		if opt.Num != num || opt.Size() != blockSize {
			t.Fatalf("block: option = %#v, want NUM %d SZX %d", opt, num, sizeExp(blockSize))
		}
		got = append(got, resp.Payload...)

		if !opt.More {
			if num != 3 {
				t.Fatalf("last block NUM = %d, want 3 for a 4 KiB body at 1 KiB", num)
			}
			break
		}
		if len(resp.Payload) != blockSize {
			t.Fatalf("block %d payload = %d bytes, want %d while M is set", num, len(resp.Payload), blockSize)
		}
		num++
	}
	if !bytes.Equal(got, blockBody(bodySize)) {
		t.Fatalf("reassembled body differs: got %d bytes, want %d", len(got), bodySize)
	}
}

// TestCoAPBlock2HonoursSmallerRequestedSize pins the other direction of RFC 7959
// section 2.4: a peer that asks for 256-byte blocks gets 256-byte blocks even
// though the server would send 1024.
func TestCoAPBlock2HonoursSmallerRequestedSize(t *testing.T) {
	const bodySize, requested = 1024, 256
	server := startBlockServer(t, bodySize, WithMaxBlockSize(1024))
	conn := dialCoAP(t, server.Addr().String())

	var got []byte
	num := uint32(0)
	for blocks := 0; blocks < 16; blocks++ {
		req := Message{Type: TypeCON, Code: CodeGet, MessageID: uint16(200 + blocks), Token: []byte{2}}
		req.Options = map[uint16][]byte{
			Block2Option: BlockOption{Num: num, SizeExp: sizeExp(requested)}.Marshal(),
		}
		resp := coapExchange(t, conn, req)
		if resp.Code != CodeContent {
			t.Fatalf("block %d: code = %d", num, resp.Code)
		}
		opt, err := parseBlockOption(resp.Options[Block2Option])
		if err != nil {
			t.Fatal(err)
		}
		if opt.Size() != requested {
			t.Fatalf("block %d: size = %d, want the requested %d", num, opt.Size(), requested)
		}
		got = append(got, resp.Payload...)
		if !opt.More {
			break
		}
		num++
	}
	if !bytes.Equal(got, blockBody(bodySize)) {
		t.Fatalf("reassembled %d bytes, want %d", len(got), bodySize)
	}
}

// TestCoAPBlock2OutOfRangeIsBadOption: asking for a block past the end is a
// client error, not an empty success (RFC 7959 section 2.9.2).
func TestCoAPBlock2OutOfRangeIsBadOption(t *testing.T) {
	server := startBlockServer(t, 1024, WithMaxBlockSize(1024))
	conn := dialCoAP(t, server.Addr().String())

	req := Message{Type: TypeCON, Code: CodeGet, MessageID: 300, Token: []byte{3}}
	req.Options = map[uint16][]byte{
		Block2Option: BlockOption{Num: 99, SizeExp: sizeExp(1024)}.Marshal(),
	}
	resp := coapExchange(t, conn, req)
	if resp.Code != CodeBadOption {
		t.Fatalf("code = %d, want 4.02 Bad Option", resp.Code)
	}
	if len(resp.Payload) != 0 {
		t.Fatalf("payload = %q, want none", resp.Payload)
	}
}

// TestCoAPBlockWiseOffKeepsSingleDatagram guards the default: enabling nothing
// must leave the wire format of a large response exactly as it was.
func TestCoAPBlockWiseOffKeepsSingleDatagram(t *testing.T) {
	const bodySize = 4096
	server := startBlockServer(t, bodySize)
	conn := dialCoAP(t, server.Addr().String())

	req := Message{Type: TypeCON, Code: CodeGet, MessageID: 400, Token: []byte{4}}
	resp := coapExchange(t, conn, req)
	if _, ok := resp.Options[Block2Option]; ok {
		t.Fatal("Block2 option appeared although block-wise transfer is off")
	}
	if !bytes.Equal(resp.Payload, blockBody(bodySize)) {
		t.Fatalf("payload = %d bytes, want the whole %d-byte body", len(resp.Payload), bodySize)
	}
}

// TestCoAPBlock1Refused: block-wise uploads are not implemented, and a server
// that ignores Block1 would run the handler once per fragment with a partial
// body. 4.13 with a Block1 hint is the RFC 7959 section 2.9.3 way to say so.
func TestCoAPBlock1Refused(t *testing.T) {
	handled := make(chan struct{}, 1)
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithResponder(func(core.Session, core.Message) ([]byte, error) {
			handled <- struct{}{}
			return []byte("written"), nil
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopGateway(t, gateway) })

	conn := dialCoAP(t, server.Addr().String())
	req := Message{Type: TypeCON, Code: CodePut, MessageID: 500, Token: []byte{5}, Payload: []byte("frag")}
	req.Options = map[uint16][]byte{
		Block1Option: BlockOption{Num: 0, More: true, SizeExp: sizeExp(1024)}.Marshal(),
	}
	resp := coapExchange(t, conn, req)
	if resp.Code != CodeRequestEntityTooLarge {
		t.Fatalf("code = %d, want 4.13 Request Entity Too Large", resp.Code)
	}
	if _, err := parseBlockOption(resp.Options[Block1Option]); err != nil {
		t.Fatalf("response carries no usable Block1 hint: %v", err)
	}
	select {
	case <-handled:
		t.Fatal("the responder ran for a block-wise upload; a fragment would have been applied as a whole body")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestCoAPBlock2DedupKeepsTheSameBlock: a retransmitted block request is answered
// from the dedup cache, so it must return the same bytes rather than re-slicing.
func TestCoAPBlock2DedupKeepsTheSameBlock(t *testing.T) {
	server := startBlockServer(t, 4096, WithMaxBlockSize(1024))
	conn := dialCoAP(t, server.Addr().String())

	req := Message{Type: TypeCON, Code: CodeGet, MessageID: 600, Token: []byte{6}}
	first := coapExchange(t, conn, req)
	// Same MessageID: the server treats it as a retransmission.
	second := coapExchange(t, conn, req)
	if !bytes.Equal(first.Payload, second.Payload) {
		t.Fatalf("retransmitted block request returned %d bytes, first returned %d",
			len(second.Payload), len(first.Payload))
	}
	opt, err := parseBlockOption(second.Options[Block2Option])
	if err != nil || !opt.More {
		t.Fatalf("cached response lost its Block2 option: %#v, %v", opt, err)
	}
}

// BenchmarkBlockFor measures the per-response cost block-wise adds to the
// normal path: one option lookup, one division and a slice header.
func BenchmarkBlockFor(b *testing.B) {
	body := blockBody(4096)
	req := blockRequest{present: true, offset: 2048, size: 1024}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		chunk, opt, ok := blockFor(body, req)
		if !ok || opt == nil || len(chunk) != 1024 {
			b.Fatalf("blockFor = %d bytes, ok=%v", len(chunk), ok)
		}
	}
}

// BenchmarkParseBlockOption is the decode side, called once per request when
// block-wise is enabled.
func BenchmarkParseBlockOption(b *testing.B) {
	wire := BlockOption{Num: 3, More: true, SizeExp: 6}.Marshal()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := parseBlockOption(wire); err != nil {
			b.Fatal(err)
		}
	}
}
