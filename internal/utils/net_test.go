package utils

import (
	"net"
	"testing"
)

func TestParseIP(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"192.168.1.1:8080", "192.168.1.1"},
		{"[::1]:8080", "::1"},
		{"10.0.0.1", "10.0.0.1"}, // no port
	}
	for _, tt := range tests {
		got := ParseIP(tt.input)
		if got != tt.want {
			t.Errorf("ParseIP(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIPToKey(t *testing.T) {
	v4 := net.ParseIP("10.0.0.1")
	if got := IPToKey(v4); got != "10.0.0.1" {
		t.Errorf("IPToKey(v4) = %q, want %q", got, "10.0.0.1")
	}

	v6 := net.ParseIP("::1")
	if got := IPToKey(v6); got != "::1" {
		t.Errorf("IPToKey(v6) = %q, want %q", got, "::1")
	}
}

func TestExtractIPFromAddr(t *testing.T) {
	tcpAddr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
	if got := ExtractIPFromAddr(tcpAddr); !got.Equal(net.ParseIP("127.0.0.1")) {
		t.Errorf("ExtractIPFromAddr(TCP) = %v, want 127.0.0.1", got)
	}

	udpAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 9090}
	if got := ExtractIPFromAddr(udpAddr); !got.Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("ExtractIPFromAddr(UDP) = %v, want 10.0.0.1", got)
	}
}

func TestIsPrivateIP(t *testing.T) {
	privates := []string{"10.0.0.1", "172.16.5.5", "192.168.1.1", "127.0.0.1"}
	for _, ip := range privates {
		if !IsPrivateIP(net.ParseIP(ip)) {
			t.Errorf("IsPrivateIP(%s) = false, want true", ip)
		}
	}

	publics := []string{"8.8.8.8", "1.1.1.1", "203.0.113.5"}
	for _, ip := range publics {
		if IsPrivateIP(net.ParseIP(ip)) {
			t.Errorf("IsPrivateIP(%s) = true, want false", ip)
		}
	}
}

func TestNormalizeIP(t *testing.T) {
	got := NormalizeIP("10.0.0.1")
	if got != "10.0.0.1" {
		t.Errorf("NormalizeIP = %q, want %q", got, "10.0.0.1")
	}

	// Invalid IP returns trimmed input.
	got = NormalizeIP("  not-an-ip  ")
	if got != "not-an-ip" {
		t.Errorf("NormalizeIP(invalid) = %q, want %q", got, "not-an-ip")
	}
}
