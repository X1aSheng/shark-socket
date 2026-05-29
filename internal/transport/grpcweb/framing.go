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
		trailers.WriteString(message)
		trailers.WriteString("\r\n")
	}
	payload := []byte(trailers.String())
	dst = append(dst, trailerFrameFlag)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(payload)))
	return append(dst, payload...)
}
