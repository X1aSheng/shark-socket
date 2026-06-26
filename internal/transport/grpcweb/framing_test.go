package grpcweb

import (
	"bytes"
	"net/http"
	"testing"
)

func TestFramingRoundTrip(t *testing.T) {
	payload := []byte("hello grpc-web")
	frame := appendDataFrame(nil, payload)
	frame = appendTrailerFrame(frame, 0, "")

	data, isTrailer, err := parseRequestPayload(frame, false)
	if err != nil {
		t.Fatalf("parseRequestPayload: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("data = %q, want %q", data, payload)
	}
	if !isTrailer {
		t.Error("expected isTrailer = true")
	}
}

func TestFramingMultipleDataFrames(t *testing.T) {
	f1 := appendDataFrame(nil, []byte("part1"))
	f2 := appendDataFrame(f1, []byte("part2"))
	f2 = appendTrailerFrame(f2, 0, "")

	data, isTrailer, err := parseRequestPayload(f2, false)
	if err != nil {
		t.Fatalf("parseRequestPayload: %v", err)
	}
	if string(data) != "part1part2" {
		t.Fatalf("data = %q, want part1part2", data)
	}
	if !isTrailer {
		t.Error("expected isTrailer = true")
	}
}

func TestIsGRPCWebRequest(t *testing.T) {
	r := func(ct string) *http.Request {
		return &http.Request{Header: http.Header{"Content-Type": {ct}}}
	}
	if !isGRPCWebRequest(r("application/grpc-web")) {
		t.Error("expected application/grpc-web to be detected")
	}
	if !isGRPCWebRequest(r("application/grpc-web+proto")) {
		t.Error("expected application/grpc-web+proto to be detected")
	}
	if isGRPCWebRequest(r("application/json")) {
		t.Error("application/json should not be detected")
	}
}

func TestParseStrictMode(t *testing.T) {
	// Strict mode: truncated frame returns an error.
	badFrame := []byte{0x00, 0x00, 0x00, 0x00, 0x10} // claims 16 bytes but no data
	_, _, err := parseRequestPayload(badFrame, true)
	if err == nil {
		t.Error("expected error in strict mode for truncated frame")
	}
	// Non-strict mode: silently returns raw body for bad data.
	data, isTrailer, err := parseRequestPayload(badFrame, false)
	if err != nil {
		t.Fatalf("non-strict parse: %v", err)
	}
	if isTrailer {
		t.Error("expected isTrailer = false for truncated data in non-strict mode")
	}
	if !bytes.Equal(data, badFrame) {
		t.Fatalf("non-strict data = %v, want raw body", data)
	}
}

func TestAppendDataFrame(t *testing.T) {
	dst := appendDataFrame(nil, []byte("abc"))
	// 1 flag + 4 len + 3 data = 8
	if len(dst) != 8 {
		t.Fatalf("frame len = %d, want 8", len(dst))
	}
	if dst[0] != dataFrameFlag {
		t.Fatalf("flag = %d, want %d", dst[0], dataFrameFlag)
	}
}

func TestAppendTrailerFrame(t *testing.T) {
	dst := appendTrailerFrame(nil, 0, "")
	// 5 header + 16 payload ("grpc-status: 0\r\n") = 21
	expectedLen := 5 + len("grpc-status: 0\r\n")
	if len(dst) != expectedLen {
		t.Fatalf("trailer len = %d, want %d", len(dst), expectedLen)
	}
	if dst[0] != trailerFrameFlag {
		t.Fatalf("flag = %d, want %d", dst[0], trailerFrameFlag)
	}
}
