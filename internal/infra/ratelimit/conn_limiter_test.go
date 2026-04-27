package ratelimit

import (
	"net"
	"testing"
	"time"
)

func TestNewConnectionLimiter(t *testing.T) {
	cl := NewConnectionLimiter(10, time.Minute)
	if cl == nil {
		t.Fatal("expected non-nil limiter")
	}
	if cl.rate != 10 {
		t.Errorf("expected rate 10, got %d", cl.rate)
	}
	if cl.window != time.Minute {
		t.Errorf("expected window 1m, got %v", cl.window)
	}
}

func TestConnectionLimiter_Allow(t *testing.T) {
	cl := NewConnectionLimiter(3, time.Minute)
	ip := "192.168.1.1"

	// First 3 connections should be allowed
	for i := 0; i < 3; i++ {
		if !cl.Allow(ip) {
			t.Errorf("connection %d should be allowed", i+1)
		}
	}

	// 4th connection should be rejected
	if cl.Allow(ip) {
		t.Error("4th connection should be rejected")
	}

	// Count should be 3
	if count := cl.Count(ip); count != 3 {
		t.Errorf("expected count 3, got %d", count)
	}
}

func TestConnectionLimiter_WindowExpiry(t *testing.T) {
	cl := NewConnectionLimiter(2, 100*time.Millisecond)
	ip := "192.168.1.2"

	// Allow 2 connections
	if !cl.Allow(ip) {
		t.Error("first connection should be allowed")
	}
	if !cl.Allow(ip) {
		t.Error("second connection should be allowed")
	}

	// 3rd should be rejected
	if cl.Allow(ip) {
		t.Error("third connection should be rejected")
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Should be allowed again (new window)
	if !cl.Allow(ip) {
		t.Error("connection after window expiry should be allowed")
	}
}

func TestConnectionLimiter_AllowAddr(t *testing.T) {
	cl := NewConnectionLimiter(2, time.Minute)

	// Create a mock addr
	addr, _ := net.ResolveTCPAddr("tcp", "192.168.1.100:12345")

	// First 2 should be allowed
	for i := 0; i < 2; i++ {
		if !cl.AllowAddr(addr) {
			t.Errorf("connection %d should be allowed", i+1)
		}
	}

	// 3rd should be rejected
	if cl.AllowAddr(addr) {
		t.Error("3rd connection from same IP should be rejected")
	}
}

func TestConnectionLimiter_Remove(t *testing.T) {
	cl := NewConnectionLimiter(2, time.Minute)
	ip := "192.168.1.3"

	// Allow 2 connections
	cl.Allow(ip)
	cl.Allow(ip)

	// Remove one
	cl.Remove(ip)

	// Should be able to connect again
	if !cl.Allow(ip) {
		t.Error("connection after remove should be allowed")
	}
}

func TestConnectionLimiter_DifferentIPs(t *testing.T) {
	cl := NewConnectionLimiter(1, time.Minute)

	// Each IP should be limited independently
	for i := 0; i < 5; i++ {
		ip := "10.0.0." + string(rune('1'+i))
		if !cl.Allow(ip) {
			t.Errorf("first connection from %s should be allowed", ip)
		}
		if cl.Allow(ip) {
			t.Errorf("second connection from %s should be rejected", ip)
		}
	}
}

func TestConnectionLimiter_SetRate(t *testing.T) {
	cl := NewConnectionLimiter(2, time.Minute)
	ip := "192.168.1.5"

	// First connection
	cl.Allow(ip)

	// Change the rate to higher value
	cl.SetRate(5)

	// Should now allow more connections (already have 1)
	for i := 1; i < 5; i++ {
		if !cl.Allow(ip) {
			t.Errorf("connection %d should be allowed with new rate", i)
		}
	}
}

func TestConnectionLimiter_Reset(t *testing.T) {
	cl := NewConnectionLimiter(1, time.Minute)
	ip := "192.168.1.6"

	cl.Allow(ip)
	cl.Allow(ip) // rejected

	cl.Reset()

	if !cl.Allow(ip) {
		t.Error("connection after reset should be allowed")
	}
}

func TestConnectionLimiter_ActiveCount(t *testing.T) {
	cl := NewConnectionLimiter(3, time.Minute)

	if count := cl.ActiveCount(); count != 0 {
		t.Errorf("expected 0 active IPs, got %d", count)
	}

	cl.Allow("192.168.1.1")
	cl.Allow("192.168.1.2")

	if count := cl.ActiveCount(); count != 2 {
		t.Errorf("expected 2 active IPs, got %d", count)
	}
}

func TestConnectionLimiter_AllowNilAddr(t *testing.T) {
	cl := NewConnectionLimiter(1, time.Minute)

	// Should allow nil address
	if !cl.AllowAddr(nil) {
		t.Error("nil address should be allowed")
	}
}

func TestConnectionLimiter_AllowInvalidAddr(t *testing.T) {
	cl := NewConnectionLimiter(1, time.Minute)

	// Create an invalid addr (no port)
	addr := &net.TCPAddr{IP: net.ParseIP("192.168.1.1"), Port: 0}

	// Should allow (graceful handling)
	if !cl.AllowAddr(addr) {
		t.Error("invalid address should be allowed")
	}
}