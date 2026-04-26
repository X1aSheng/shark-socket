package plugin

import (
	"net"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/errs"
)

func TestRateLimitPlugin_UnderLimitPasses(t *testing.T) {
	// rate=100, burst=10 gives 10 immediate tokens
	p := NewRateLimitPlugin(100, 10)
	defer p.Close()

	sess := newMockSession(1, "192.168.1.1")
	if err := p.OnAccept(sess); err != nil {
		t.Fatalf("expected nil when under limit, got %v", err)
	}
}

func TestRateLimitPlugin_OverGlobalLimit_ReturnsErrBlock(t *testing.T) {
	// rate=1000, burst=2 gives only 2 tokens before refill
	p := NewRateLimitPlugin(1000, 2)
	defer p.Close()

	// Use up both tokens
	sess1 := newMockSession(1, "10.0.0.1")
	sess2 := newMockSession(2, "10.0.0.2")
	if err := p.OnAccept(sess1); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if err := p.OnAccept(sess2); err != nil {
		t.Fatalf("second accept: %v", err)
	}

	// Third should be blocked
	sess3 := newMockSession(3, "10.0.0.3")
	if err := p.OnAccept(sess3); !isErrBlock(err) {
		t.Fatalf("expected ErrBlock when over global limit, got %v", err)
	}
}

func TestRateLimitPlugin_OverPerIPMessageLimit_ReturnsErrDrop(t *testing.T) {
	// Global rate and burst high enough; per-IP message rate very low.
	// OnAccept also consumes from the per-IP bucket, so with burst=3:
	//   OnAccept uses 1 token, OnMessage uses 1 each.
	p := NewRateLimitPlugin(1000, 100, WithRateLimitMessageRate(1000, 3))
	defer p.Close()

	sess := newMockSession(1, "192.168.1.50")
	if err := p.OnAccept(sess); err != nil {
		t.Fatalf("accept: %v", err)
	}

	data := []byte("hello")
	// Token 2 of 3
	if _, err := p.OnMessage(sess, data); err != nil {
		t.Fatalf("first message: %v", err)
	}
	// Token 3 of 3
	if _, err := p.OnMessage(sess, data); err != nil {
		t.Fatalf("second message: %v", err)
	}

	// Fourth consumption (burst exhausted) should be dropped
	out, err := p.OnMessage(sess, data)
	if !isErrDrop(err) {
		t.Fatalf("expected ErrDrop when over per-IP limit, got %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil data on drop, got %q", out)
	}
}

func TestRateLimitPlugin_NamePriority(t *testing.T) {
	p := NewRateLimitPlugin(1, 1)
	defer p.Close()

	if p.Name() != "ratelimit" {
		t.Fatalf("expected name 'ratelimit', got %q", p.Name())
	}
	if p.Priority() != 10 {
		t.Fatalf("expected priority 10, got %d", p.Priority())
	}
}

func TestRateLimitPlugin_ViolationsRecorded(t *testing.T) {
	p := NewRateLimitPlugin(1000, 1)
	defer p.Close()

	sess := newMockSession(1, "5.6.7.8")
	_ = p.OnAccept(sess) // uses the 1 token

	// This should trigger a violation and block
	sess2 := newMockSession(2, "5.6.7.8")
	_ = p.OnAccept(sess2)

	// The second accept should have recorded a violation
	// Extract the IP key to check
	ip := extractIPFromMock(sess2)
	if p.GetViolations(ip) < 1 {
		t.Fatalf("expected at least 1 violation, got %d", p.GetViolations(ip))
	}
}

func TestRateLimitPlugin_OnCloseNoop(t *testing.T) {
	p := NewRateLimitPlugin(1, 1)
	defer p.Close()

	sess := newMockSession(1, "127.0.0.1")
	// Should not panic
	p.OnClose(sess)
}

func isErrDrop(err error) bool {
	return err != nil && err == errs.ErrDrop
}

func extractIPFromMock(sess *mockRawSession) string {
	return sess.RemoteAddr().(*net.TCPAddr).IP.String()
}
