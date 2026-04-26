package plugin

import (
	"testing"
)

func TestAutoBanPlugin_NoBanUnderThreshold(t *testing.T) {
	bl := NewBlacklistPlugin()
	defer bl.Close()

	p := NewAutoBanPlugin(bl)
	defer p.Close()

	sess := newMockSession(1, "192.168.1.1")
	if err := p.OnAccept(sess); err != nil {
		t.Fatalf("OnAccept should not block under threshold, got %v", err)
	}

	// Verify the IP is not blacklisted
	sess2 := newMockSession(2, "192.168.1.1")
	if err := bl.OnAccept(sess2); err != nil {
		t.Fatalf("IP should not be blacklisted under threshold, got %v", err)
	}
}

func TestAutoBanPlugin_RateLimitViolationsTriggerBan(t *testing.T) {
	bl := NewBlacklistPlugin()
	defer bl.Close()

	thresholds := DefaultAutoBanThresholds()
	thresholds.RateLimitThreshold = 3
	thresholds.BanTTL = 0 // permanent ban for test

	p := NewAutoBanPlugin(bl, WithAutoBanThresholds(thresholds))
	defer p.Close()

	ip := "10.0.0.50"

	// Record violations up to threshold
	for i := int64(0); i < thresholds.RateLimitThreshold; i++ {
		p.RecordRateLimit(ip)
	}

	// Next OnAccept should trigger checkAndBan, which calls blacklist.Add
	sess := newMockSession(1, ip)
	if err := p.OnAccept(sess); err != nil {
		t.Fatalf("OnAccept returned error: %v", err)
	}

	// Verify the IP is now blacklisted
	sess2 := newMockSession(2, ip)
	if err := bl.OnAccept(sess2); err == nil {
		t.Fatal("expected IP to be blacklisted after exceeding rate limit threshold")
	}
}

func TestAutoBanPlugin_EmptyConnThreshold(t *testing.T) {
	bl := NewBlacklistPlugin()
	defer bl.Close()

	thresholds := DefaultAutoBanThresholds()
	thresholds.EmptyConnThreshold = 2
	thresholds.BanTTL = 0

	p := NewAutoBanPlugin(bl, WithAutoBanThresholds(thresholds))
	defer p.Close()

	ip := "10.0.0.99"

	// OnAccept increments emptyConns each time
	sess1 := newMockSession(1, ip)
	_ = p.OnAccept(sess1) // emptyConns = 1, not yet at threshold

	// Should not be banned yet
	sessCheck := newMockSession(99, ip)
	if err := bl.OnAccept(sessCheck); err != nil {
		t.Fatal("IP should not be banned after 1 empty conn")
	}

	// Second OnAccept from same IP -> emptyConns = 2, hits threshold
	sess2 := newMockSession(2, ip)
	_ = p.OnAccept(sess2) // triggers ban

	// Verify banned
	sess3 := newMockSession(3, ip)
	if err := bl.OnAccept(sess3); err == nil {
		t.Fatal("expected IP to be blacklisted after exceeding empty conn threshold")
	}
}

func TestAutoBanPlugin_ProtocolErrorThreshold(t *testing.T) {
	bl := NewBlacklistPlugin()
	defer bl.Close()

	thresholds := DefaultAutoBanThresholds()
	thresholds.ProtocolErrorThreshold = 2
	thresholds.BanTTL = 0

	p := NewAutoBanPlugin(bl, WithAutoBanThresholds(thresholds))
	defer p.Close()

	ip := "10.0.0.77"

	p.RecordProtocolError(ip)
	p.RecordProtocolError(ip)

	// Trigger check via OnMessage
	sess := newMockSession(1, ip)
	_, _ = p.OnMessage(sess, []byte("data"))

	// Verify banned
	sess2 := newMockSession(2, ip)
	if err := bl.OnAccept(sess2); err == nil {
		t.Fatal("expected IP to be blacklisted after exceeding protocol error threshold")
	}
}

func TestAutoBanPlugin_NamePriority(t *testing.T) {
	bl := NewBlacklistPlugin()
	defer bl.Close()

	p := NewAutoBanPlugin(bl)
	defer p.Close()

	if p.Name() != "autoban" {
		t.Fatalf("expected name 'autoban', got %q", p.Name())
	}
	if p.Priority() != 20 {
		t.Fatalf("expected priority 20, got %d", p.Priority())
	}
}

func TestAutoBanPlugin_OnMessagePassthrough(t *testing.T) {
	bl := NewBlacklistPlugin()
	defer bl.Close()

	p := NewAutoBanPlugin(bl)
	defer p.Close()

	sess := newMockSession(1, "192.168.1.1")
	data := []byte("hello")
	out, err := p.OnMessage(sess, data)
	if err != nil {
		t.Fatalf("OnMessage error: %v", err)
	}
	if string(out) != string(data) {
		t.Fatalf("expected data passthrough, got %q", out)
	}
}
