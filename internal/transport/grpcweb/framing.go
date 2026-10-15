package grpcweb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const (
	dataFrameFlag    byte = 0x00
	trailerFrameFlag byte = 0x80
	frameHeaderSize       = 5
)

func isGRPCWebRequest(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("content-type")), "application/grpc-web")
}

func parseRequestPayload(body []byte, strict bool) ([]byte, bool, error) {
	if len(body) < frameHeaderSize {
		return body, false, nil
	}
	payload, ok, err := parseDataFrames(body)
	if err != nil {
		if !strict {
			return body, false, nil
		}
		return nil, false, err
	}
	if !ok {
		return body, false, nil
	}
	return payload, true, nil
}

func parseDataFrames(body []byte) ([]byte, bool, error) {
	var out bytes.Buffer
	framed := false
	for len(body) > 0 {
		if len(body) < frameHeaderSize {
			return nil, false, fmt.Errorf("grpc-web frame header truncated")
		}
		flag := body[0]
		size := int(binary.BigEndian.Uint32(body[1:frameHeaderSize]))
		body = body[frameHeaderSize:]
		if size < 0 || len(body) < size {
			return nil, false, fmt.Errorf("grpc-web frame payload truncated")
		}
		payload := body[:size]
		body = body[size:]

		switch flag {
		case dataFrameFlag:
			framed = true
			out.Write(payload)
		case trailerFrameFlag:
			framed = true
		default:
			return nil, false, fmt.Errorf("unsupported grpc-web frame flag 0x%02x", flag)
		}
	}
	if !framed {
		return nil, false, nil
	}
	return out.Bytes(), true, nil
}

func appendDataFrame(dst, payload []byte) []byte {
	dst = append(dst, dataFrameFlag)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(payload)))
	return append(dst, payload...)
}

func appendTrailerFrame(dst []byte, status int, message string) []byte {
	var trailers strings.Builder
	trailers.WriteString("grpc-status: ")
	trailers.WriteString(strconv.Itoa(status))
	trailers.WriteString("\r\n")
	if message != "" {
		trailers.WriteString("grpc-message: ")
		trailers.WriteString(encodeGRPCMessage(message))
		trailers.WriteString("\r\n")
	}
	payload := []byte(trailers.String())
	dst = append(dst, trailerFrameFlag)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(payload)))
	return append(dst, payload...)
}

const upperHex = "0123456789ABCDEF"

// grpcWebSafeByte reports whether c may appear literally in a grpc-message
// value: the gRPC-Web wire specification passes through %x20-%x24 and %x26-%x7E
// and requires percent-encoding for everything else.
func grpcWebSafeByte(c byte) bool {
	return (c >= 0x20 && c <= 0x24) || (c >= 0x26 && c <= 0x7e)
}

// encodeGRPCMessage percent-encodes a grpc-message value as required by the
// gRPC wire specification: only bytes in %x20-%x24 and %x26-%x7E may appear
// unencoded, everything else (including the percent sign itself) is written
// as "%XX". Without this, a message containing CR, LF or non-ASCII bytes
// would corrupt the trailer block (header injection) and could not be decoded
// by the client.
func encodeGRPCMessage(s string) string {
	needsEscape := false
	for i := 0; i < len(s); i++ {
		if !grpcWebSafeByte(s[i]) {
			needsEscape = true
			break
		}
	}
	if !needsEscape {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) * 3)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if grpcWebSafeByte(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperHex[c>>4])
		b.WriteByte(upperHex[c&0x0f])
	}
	return b.String()
}
