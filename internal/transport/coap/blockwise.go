package coap

import (
	"errors"
	"fmt"
)

// RFC 7959 option numbers. Block1 carries a request body in blocks, Block2
// carries a response body; both share the same option value format.
const (
	Block2Option uint16 = 23
	Block1Option uint16 = 27
)

// ErrInvalidBlockOption reports a Block1/Block2 option that cannot be decoded
// per RFC 7959 section 2.2: more than three value bytes, or the reserved SZX
// value 7.
var ErrInvalidBlockOption = errors.New("invalid coap block option")

// BlockOption is the decoded value of a Block1/Block2 option: the
// NUM/M/SZX triple of RFC 7959 section 2.1 packed into up to three bytes.
type BlockOption struct {
	// Num is the block sequence number, counted in units of Size().
	Num uint32
	// More is the M bit: "more blocks follow". The last block carries false.
	More bool
	// SizeExp is SZX: the block size is 1 << (SizeExp + 4), i.e. 16 to 1024.
	SizeExp uint8
}

const (
	// minBlockSizeExp is SZX 0 (16 bytes), the smallest legal block.
	minBlockSizeExp uint8 = 0
	// maxBlockSizeExp is SZX 6 (1024 bytes). RFC 7959 section 2.2 reserves
	// SZX 7, so 6 is the largest size a server may offer.
	maxBlockSizeExp uint8 = 6
	// DefaultBlockSize is the block size used when a peer does not ask for a
	// specific one. 1024 keeps a block well inside a 1280-byte IPv6 MTU.
	DefaultBlockSize = 1024
)

// Size returns the block size in bytes.
func (b BlockOption) Size() int { return 1 << (b.SizeExp + 4) }

// Marshal encodes the option value in the minimal number of bytes, as RFC 7959
// section 2.2 requires: the leading zero bytes of NUM must be elided, and NUM 0
// with M=0 and SZX=0 is the empty value.
func (b BlockOption) Marshal() []byte {
	raw := b.Num<<4 | uint32(b.SizeExp)
	if b.More {
		raw |= 0x08
	}
	if raw == 0 {
		return nil
	}
	// Always build three bytes, then drop the leading zeros. NUM is 20 bits, so
	// 24 bits always suffice; anything above that is caller error and is
	// silently truncated to the low 24 bits rather than producing an option
	// value the wire format cannot carry.
	buf := []byte{byte(raw >> 16), byte(raw >> 8), byte(raw)}
	first := 0
	for first < len(buf)-1 && buf[first] == 0 {
		first++
	}
	return buf[first:]
}

// parseBlockOption decodes a Block1/Block2 option value. A value longer than
// three bytes, or the reserved SZX 7, is an error: RFC 7959 section 2.2 defines
// only 0..3 bytes and SZX 0..6.
func parseBlockOption(v []byte) (BlockOption, error) {
	if len(v) > 3 {
		return BlockOption{}, fmt.Errorf("%w: %d value bytes", ErrInvalidBlockOption, len(v))
	}
	var raw uint32
	for _, c := range v {
		raw = raw<<8 | uint32(c)
	}
	opt := BlockOption{
		Num:     raw >> 4,
		More:    raw&0x08 != 0,
		SizeExp: uint8(raw & 0x07),
	}
	// SZX 7 is reserved (RFC 7959 section 2.2); accepting it would mean a
	// block size of 2048 that no MTU can carry in one datagram.
	if opt.SizeExp > maxBlockSizeExp {
		return BlockOption{}, fmt.Errorf("%w: reserved SZX %d", ErrInvalidBlockOption, opt.SizeExp)
	}
	return opt, nil
}

// sizeExp returns the SZX for a power-of-two block size, or maxBlockSizeExp for
// anything that is not one of 16..1024 (callers only pass configured sizes).
func sizeExp(size int) uint8 {
	for exp := minBlockSizeExp; exp <= maxBlockSizeExp; exp++ {
		if 1<<(exp+4) == size {
			return exp
		}
	}
	return maxBlockSizeExp
}

// sliceBlock returns the chunk of body that starts at offset num*size and the
// flag saying whether anything follows it. ok is false when the offset is at or
// past the end of body, which RFC 7959 section 2.9.2 answers with 4.02 Bad
// Option.
//
// An offset exactly equal to len(body) is out of range, not an empty block: a
// body of exactly n blocks ends at block n-1 with M cleared, so block n names a
// byte that does not exist. Answering it with an empty payload would let a
// client believe the resource is one block longer than it is.
func sliceBlock(body []byte, num uint32, size int) (chunk []byte, more bool, ok bool) {
	offset := uint64(num) * uint64(size)
	if offset >= uint64(len(body)) {
		return nil, false, false
	}
	end := offset + uint64(size)
	if end >= uint64(len(body)) {
		return body[offset:], false, true
	}
	return body[offset:end], true, true
}

// blockRequest is the negotiated view of a request's Block2 option: which
// block the peer asked for, at what size, and whether it asked at all.
type blockRequest struct {
	present bool
	offset  uint64 // byte offset of the requested block in the body
	size    int    // block size to use for the response
}

// negotiateBlock2 reconciles the peer's request with the server's maximum block
// size.
//
// RFC 7959 section 2.4 lets the server answer with a smaller block than the
// peer asked for, but the block number is counted in units of the size in use,
// so the offset has to be computed against the *requested* size before being
// re-divided. Both sizes are powers of two, so the re-division is exact.
func negotiateBlock2(values map[uint16][]byte, maxSize int) (blockRequest, error) {
	v, present := values[Block2Option]
	if !present {
		return blockRequest{size: maxSize}, nil
	}
	opt, err := parseBlockOption(v)
	if err != nil {
		return blockRequest{}, err
	}
	requested := opt.Size()
	size := requested
	if size > maxSize {
		size = maxSize
	}
	offset := uint64(opt.Num) * uint64(requested)
	return blockRequest{present: true, offset: offset, size: size}, nil
}

// blockFor cuts body into the block the peer asked for. The second return is
// the Block2 option to echo, which is nil when the body fits in one datagram
// and the peer did not use the option (so a small response stays byte-identical
// to what it was before block-wise transfer existed).
func blockFor(body []byte, req blockRequest) (chunk []byte, opt *BlockOption, ok bool) {
	if !req.present && len(body) <= req.size {
		return body, nil, true
	}
	num := uint32(req.offset / uint64(req.size))
	chunk, more, ok := sliceBlock(body, num, req.size)
	if !ok {
		return nil, nil, false
	}
	return chunk, &BlockOption{Num: num, More: more, SizeExp: sizeExp(req.size)}, true
}
