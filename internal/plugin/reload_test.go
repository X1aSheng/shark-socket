package plugin

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// staticSession builds the package's fakeSession from a "host:port" string, so
// the tests read in terms of addresses rather than net.Addr literals.
func staticSession(remote string) fakeSession {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	return fakeSession{addr: &net.TCPAddr{IP: net.ParseIP(host)}}
}

func TestBlacklistReplaceSwapsSet(t *testing.T) {
	p := NewBlacklist("10.0.0.1")
	if err := p.OnAccept(staticSession("10.0.0.1:5555")); err == nil {
		t.Fatal("10.0.0.1 should be blocked before Replace")
	}
	if err := p.OnAccept(staticSession("10.0.0.2:5555")); err != nil {
		t.Fatalf("10.0.0.2 should be allowed: %v", err)
	}

	if err := p.Replace([]string{"10.0.0.2"}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	// The whole set is swapped: the previous entry is no longer blocked.
	if err := p.OnAccept(staticSession("10.0.0.1:5555")); err != nil {
		t.Fatalf("10.0.0.1 should be allowed after Replace: %v", err)
	}
	if err := p.OnAccept(staticSession("10.0.0.2:5555")); err != core.ErrPluginBlock {
		t.Fatalf("10.0.0.2 err = %v, want ErrPluginBlock", err)
	}
}

// TestBlacklistReplaceKeepsOldSetOnError is the property the reload path
// depends on: a malformed list must not leave a half-built set in force.
func TestBlacklistReplaceKeepsOldSetOnError(t *testing.T) {
	p := NewBlacklist("10.0.0.1")
	err := p.Replace([]string{"10.0.0.2", "not-an-ip"})
	if err == nil {
		t.Fatal("Replace accepted a malformed entry")
	}
	if !strings.Contains(err.Error(), "not-an-ip") {
		t.Fatalf("error %q should name the offending entry", err)
	}
	if err := p.OnAccept(staticSession("10.0.0.1:5555")); err != core.ErrPluginBlock {
		t.Fatalf("old block list was lost: err = %v", err)
	}
	if err := p.OnAccept(staticSession("10.0.0.2:5555")); err != nil {
		t.Fatalf("rejected list leaked an entry: %v", err)
	}
}

func TestValidateBlacklist(t *testing.T) {
	if err := ValidateBlacklist(nil); err != nil {
		t.Fatalf("empty list: %v", err)
	}
	if err := ValidateBlacklist([]string{"10.0.0.0/8", "::1", ""}); err != nil {
		t.Fatalf("valid list: %v", err)
	}
	if err := ValidateBlacklist([]string{"10.0.0.0/33"}); err == nil {
		t.Fatal("invalid CIDR was accepted")
	}
}

// TestRateLimitSetLimitAppliesToNextMessage documents that a limit change lands
// on the next message and that the recorded history is preserved: raising the
// limit reveals the allowance immediately, lowering it takes effect at once
// because the counter already holds the messages of the current window.
func TestRateLimitSetLimitAppliesToNextMessage(t *testing.T) {
	p := NewRateLimit(2, time.Second)
	sess := staticSession("10.0.0.1:5555")
	for i := 0; i < 2; i++ {
		if _, err := p.OnMessage(sess, []byte("x")); err != nil {
			t.Fatalf("message %d: %v", i+1, err)
		}
	}
	if _, err := p.OnMessage(sess, []byte("x")); err != core.ErrPluginDrop {
		t.Fatalf("third message err = %v, want ErrPluginDrop", err)
	}

	p.SetLimit(1, time.Second)
	if rate, window := p.Limit(); rate != 1 || window != time.Second {
		t.Fatalf("Limit() = %d/%v, want 1/1s", rate, window)
	}
	if _, err := p.OnMessage(sess, []byte("x")); err != core.ErrPluginDrop {
		t.Fatalf("tightened limit was not applied: err = %v", err)
	}

	p.SetLimit(100, time.Second)
	if _, err := p.OnMessage(sess, []byte("x")); err != nil {
		t.Fatalf("loosened limit was not applied: %v", err)
	}
}

func TestRateLimitSetLimitDefaults(t *testing.T) {
	p := NewRateLimit(0, 0)
	if rate, window := p.Limit(); rate != 1 || window != time.Second {
		t.Fatalf("Limit() = %d/%v, want the 1/1s defaults", rate, window)
	}
	p.SetLimit(-5, -time.Second)
	if rate, window := p.Limit(); rate != 1 || window != time.Second {
		t.Fatalf("Limit() = %d/%v after negative input, want the defaults", rate, window)
	}
}
