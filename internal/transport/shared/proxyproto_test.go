package shared

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// v2Header builds a v2 header with the given command, family and address
// block, followed by an empty TLV section.
func v2Header(cmd, famProto byte, block []byte) []byte {
	buf := make([]byte, 0, 16+len(block))
	buf = append(buf, proxyV2Signature[:]...)
	buf = append(buf, 0x20|cmd, famProto)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(block)))
	buf = append(buf, block...)
	return buf
}

func v2Block4(src, dst net.IP, sport, dport uint16) []byte {
	block := make([]byte, 0, 12)
	block = append(block, src.To4()...)
	block = append(block, dst.To4()...)
	block = binary.BigEndian.AppendUint16(block, sport)
	block = binary.BigEndian.AppendUint16(block, dport)
	return block
}

func v2Block6(src, dst net.IP, sport, dport uint16) []byte {
	block := make([]byte, 0, 36)
	block = append(block, src.To16()...)
	block = append(block, dst.To16()...)
	block = binary.BigEndian.AppendUint16(block, sport)
	block = binary.BigEndian.AppendUint16(block, dport)
	return block
}

func TestReadProxyHeaderV1(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		wantSrc    string
		wantDst    string
		wantErr    error
		wantNoAddr bool
	}{
		{
			name:    "tcp4",
			line:    "PROXY TCP4 203.0.113.9 198.51.100.1 5555 443\r\n",
			wantSrc: "203.0.113.9:5555",
			wantDst: "198.51.100.1:443",
		},
		{
			name:    "tcp6",
			line:    "PROXY TCP6 2001:db8::1 2001:db8::2 5555 443\r\n",
			wantSrc: "[2001:db8::1]:5555",
			wantDst: "[2001:db8::2]:443",
		},
		{
			name:       "unknown",
			line:       "PROXY UNKNOWN\r\n",
			wantNoAddr: true,
		},
		{
			name:       "unknown with trailing rubbish is ignored",
			line:       "PROXY UNKNOWN ffff::1 ffff::2 1 2\r\n",
			wantNoAddr: true,
		},
		{
			name:    "not crlf terminated",
			line:    "PROXY TCP4 203.0.113.9 198.51.100.1 5555 443\n",
			wantErr: ErrProxyHeaderInvalid,
		},
		{
			name:    "missing prefix",
			line:    "PROXYX TCP4 203.0.113.9 198.51.100.1 5555 443\r\n",
			wantErr: ErrProxyHeaderInvalid,
		},
		{
			name:    "unsupported family",
			line:    "PROXY SCTP4 203.0.113.9 198.51.100.1 5555 443\r\n",
			wantErr: ErrProxyHeaderInvalid,
		},
		{
			name:    "too few fields",
			line:    "PROXY TCP4 203.0.113.9 198.51.100.1 5555\r\n",
			wantErr: ErrProxyHeaderInvalid,
		},
		{
			name:    "port out of range",
			line:    "PROXY TCP4 203.0.113.9 198.51.100.1 5555 70000\r\n",
			wantErr: ErrProxyHeaderInvalid,
		},
		{
			name:    "family and literal disagree",
			line:    "PROXY TCP4 2001:db8::1 198.51.100.1 5555 443\r\n",
			wantErr: ErrProxyHeaderInvalid,
		},
		{
			name:    "line longer than the v1 limit",
			line:    "PROXY TCP4 203.0.113.9 198.51.100.1 5555 443" + strings.Repeat(" 0", 40) + "\r\n",
			wantErr: ErrProxyHeaderTooLong,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			br := bufio.NewReaderSize(strings.NewReader(tc.line+"PAYLOAD"), proxyHeaderBuffer)
			hdr, err := readProxyHeader(br, ProxyProtocolV1)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNoAddr {
				if hdr.source != nil || hdr.dest != nil {
					t.Fatalf("expected no address, got %v -> %v", hdr.source, hdr.dest)
				}
			} else {
				if got := hdr.source.String(); got != tc.wantSrc {
					t.Errorf("source = %s, want %s", got, tc.wantSrc)
				}
				if got := hdr.dest.String(); got != tc.wantDst {
					t.Errorf("dest = %s, want %s", got, tc.wantDst)
				}
			}
			// Whatever followed the header must still be readable.
			rest, err := io.ReadAll(br)
			if err != nil {
				t.Fatalf("read rest: %v", err)
			}
			if string(rest) != "PAYLOAD" {
				t.Errorf("payload after header = %q, want %q", rest, "PAYLOAD")
			}
		})
	}
}

func TestReadProxyHeaderV2(t *testing.T) {
	// A TLV section is legal and must be swallowed, not handed to the framer.
	withTLV := append(v2Block4(net.ParseIP("203.0.113.9"), net.ParseIP("198.51.100.1"), 5555, 443),
		0x03, 0x00, 0x04, 0xde, 0xad, 0xbe, 0xef)

	cases := []struct {
		name       string
		header     []byte
		wantSrc    string
		wantNoAddr bool
		wantErr    error
	}{
		{
			name:    "tcp4",
			header:  v2Header(proxyV2CmdProxy, proxyFamTCP4, v2Block4(net.ParseIP("203.0.113.9"), net.ParseIP("198.51.100.1"), 5555, 443)),
			wantSrc: "203.0.113.9:5555",
		},
		{
			name:    "tcp6",
			header:  v2Header(proxyV2CmdProxy, proxyFamTCP6, v2Block6(net.ParseIP("2001:db8::1"), net.ParseIP("2001:db8::2"), 5555, 443)),
			wantSrc: "[2001:db8::1]:5555",
		},
		{
			name:    "tcp4 with tlv",
			header:  v2Header(proxyV2CmdProxy, proxyFamTCP4, withTLV),
			wantSrc: "203.0.113.9:5555",
		},
		{
			name:       "local command",
			header:     v2Header(proxyV2CmdLocal, proxyFamTCP4, v2Block4(net.ParseIP("203.0.113.9"), net.ParseIP("198.51.100.1"), 5555, 443)),
			wantNoAddr: true,
		},
		{
			name:       "unspec family",
			header:     v2Header(proxyV2CmdProxy, 0x00, nil),
			wantNoAddr: true,
		},
		{
			name: "bad version nibble",
			header: func() []byte {
				h := v2Header(proxyV2CmdProxy, proxyFamTCP4, v2Block4(net.ParseIP("1.1.1.1"), net.ParseIP("2.2.2.2"), 1, 2))
				h[12] = 0x31
				return h
			}(),
			wantErr: ErrProxyHeaderVersion,
		},
		{
			name: "payload longer than the limit",
			header: func() []byte {
				h := v2Header(proxyV2CmdProxy, proxyFamTCP4, nil)
				binary.BigEndian.PutUint16(h[14:16], proxyV2MaxPayload+1)
				return h
			}(),
			wantErr: ErrProxyHeaderTooLong,
		},
		{
			name:    "address block truncated",
			header:  v2Header(proxyV2CmdProxy, proxyFamTCP4, []byte{203, 0, 113, 9}),
			wantErr: ErrProxyHeaderInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			br := bufio.NewReaderSize(bytes.NewReader(append(tc.header, []byte("PAYLOAD")...)), proxyHeaderBuffer)
			hdr, err := readProxyHeader(br, ProxyProtocolV2)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNoAddr {
				if hdr.source != nil {
					t.Fatalf("expected no address, got %v", hdr.source)
				}
			} else if got := hdr.source.String(); got != tc.wantSrc {
				t.Errorf("source = %s, want %s", got, tc.wantSrc)
			}
			rest, err := io.ReadAll(br)
			if err != nil {
				t.Fatalf("read rest: %v", err)
			}
			if string(rest) != "PAYLOAD" {
				t.Errorf("payload after header = %q, want %q", rest, "PAYLOAD")
			}
		})
	}
}

func TestReadProxyHeaderVersionMismatch(t *testing.T) {
	v1 := []byte("PROXY TCP4 203.0.113.9 198.51.100.1 5555 443\r\n")
	v2 := v2Header(proxyV2CmdProxy, proxyFamTCP4, v2Block4(net.ParseIP("203.0.113.9"), net.ParseIP("198.51.100.1"), 5555, 443))

	t.Run("v2 listener rejects v1", func(t *testing.T) {
		br := bufio.NewReaderSize(bytes.NewReader(v1), proxyHeaderBuffer)
		if _, err := readProxyHeader(br, ProxyProtocolV2); !errors.Is(err, ErrProxyHeaderVersion) {
			t.Fatalf("err = %v, want ErrProxyHeaderVersion", err)
		}
	})
	t.Run("v1 listener rejects v2", func(t *testing.T) {
		br := bufio.NewReaderSize(bytes.NewReader(v2), proxyHeaderBuffer)
		if _, err := readProxyHeader(br, ProxyProtocolV1); !errors.Is(err, ErrProxyHeaderVersion) {
			t.Fatalf("err = %v, want ErrProxyHeaderVersion", err)
		}
	})
	t.Run("auto accepts both", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			data []byte
		}{{"v1", v1}, {"v2", v2}} {
			br := bufio.NewReaderSize(bytes.NewReader(tc.data), proxyHeaderBuffer)
			if _, err := readProxyHeader(br, ProxyProtocolAuto); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		}
	})
	t.Run("auto rejects neither", func(t *testing.T) {
		br := bufio.NewReaderSize(strings.NewReader("GET / HTTP/1.1\r\n\r\n"), proxyHeaderBuffer)
		if _, err := readProxyHeader(br, ProxyProtocolAuto); !errors.Is(err, ErrProxyHeaderInvalid) {
			t.Fatalf("err = %v, want ErrProxyHeaderInvalid", err)
		}
	})
}

func TestNewProxyListenerOffReturnsSame(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	if got := NewProxyListener(ln, ProxyConfig{}); got != ln {
		t.Fatalf("off mode should return the listener unchanged, got %T", got)
	}
}

func TestProxyListenerReportsHeaderAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	pl := NewProxyListener(ln, ProxyConfig{Mode: ProxyProtocolAuto, Timeout: 2 * time.Second})

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Header and payload in one write: the payload is what proves the parser
	// stops exactly at the header.
	_, err = client.Write([]byte("PROXY TCP4 203.0.113.9 198.51.100.1 5555 443\r\nPING"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	conn, err := pl.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if got := conn.RemoteAddr().String(); got != "203.0.113.9:5555" {
		t.Errorf("RemoteAddr = %s, want 203.0.113.9:5555", got)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(buf) != "PING" {
		t.Errorf("payload = %q, want PING", buf)
	}
}

func TestProxyListenerSkipsBadHeaderAndKeepsServing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	var mu sync.Mutex
	var rejected []error
	pl := NewProxyListener(ln, ProxyConfig{
		Mode:    ProxyProtocolV1,
		Timeout: 2 * time.Second,
		OnReject: func(_ net.Addr, err error) {
			mu.Lock()
			rejected = append(rejected, err)
			mu.Unlock()
		},
	})

	bad, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial bad: %v", err)
	}
	defer func() { _ = bad.Close() }()
	if _, err := bad.Write([]byte("GET / HTTP/1.1\r\n\r\n")); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	good, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial good: %v", err)
	}
	defer func() { _ = good.Close() }()
	if _, err := good.Write([]byte("PROXY TCP4 203.0.113.9 198.51.100.1 5555 443\r\nOK")); err != nil {
		t.Fatalf("write good: %v", err)
	}

	conn, err := pl.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if got := conn.RemoteAddr().String(); got != "203.0.113.9:5555" {
		t.Fatalf("accepted the wrong connection: %s", got)
	}

	// The rejected connection must be closed, not left dangling.
	_ = bad.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := bad.Read(make([]byte, 1)); err == nil {
		t.Fatal("rejected connection was not closed")
	}

	mu.Lock()
	n := len(rejected)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("OnReject called %d times, want 1", n)
	}
}

func TestProxyListenerTimesOutSilentClient(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	pl := NewProxyListener(ln, ProxyConfig{Mode: ProxyProtocolAuto, Timeout: 50 * time.Millisecond})

	silent, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = silent.Close() }()

	done := make(chan error, 1)
	go func() {
		_, err := pl.Accept()
		done <- err
	}()

	// The listener skips the silent connection, so it only returns once the
	// listener itself is closed.
	select {
	case err := <-done:
		t.Fatalf("Accept returned early: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	_ = ln.Close()
	if err := <-done; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after close = %v, want net.ErrClosed", err)
	}
}
