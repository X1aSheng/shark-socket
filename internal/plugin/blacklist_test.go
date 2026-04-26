package plugin

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
)

func TestBlacklistPlugin_ExactIPBlocking(t *testing.T) {
	p := NewBlacklistPlugin("192.168.1.100")
	defer p.Close()

	sess := newMockSession(1, "192.168.1.100")
	if err := p.OnAccept(sess); !isErrBlock(err) {
		t.Fatalf("expected ErrBlock for blacklisted IP, got %v", err)
	}
}

func TestBlacklistPlugin_CIDRRangeBlocking(t *testing.T) {
	p := NewBlacklistPlugin("10.0.0.0/8")
	defer p.Close()

	sess := newMockSession(1, "10.1.2.3")
	if err := p.OnAccept(sess); !isErrBlock(err) {
		t.Fatalf("expected ErrBlock for IP in CIDR range, got %v", err)
	}
}

func TestBlacklistPlugin_NonBlockedIPPassthrough(t *testing.T) {
	p := NewBlacklistPlugin("192.168.1.100")
	defer p.Close()

	sess := newMockSession(1, "192.168.1.200")
	if err := p.OnAccept(sess); err != nil {
		t.Fatalf("expected nil for non-blocked IP, got %v", err)
	}
}

func TestBlacklistPlugin_AddRemove(t *testing.T) {
	p := NewBlacklistPlugin()
	defer p.Close()

	// Initially not blocked
	sess := newMockSession(1, "1.2.3.4")
	if err := p.OnAccept(sess); err != nil {
		t.Fatalf("expected nil before add, got %v", err)
	}

	// Add the IP
	p.Add("1.2.3.4", 0)
	if err := p.OnAccept(sess); !isErrBlock(err) {
		t.Fatalf("expected ErrBlock after add, got %v", err)
	}

	// Remove the IP
	p.Remove("1.2.3.4")
	sess2 := newMockSession(2, "1.2.3.4")
	if err := p.OnAccept(sess2); err != nil {
		t.Fatalf("expected nil after remove, got %v", err)
	}
}

func TestBlacklistPlugin_AddCIDR(t *testing.T) {
	p := NewBlacklistPlugin()
	defer p.Close()

	p.Add("172.16.0.0/12", 0)

	sess := newMockSession(1, "172.20.5.6")
	if err := p.OnAccept(sess); !isErrBlock(err) {
		t.Fatalf("expected ErrBlock for IP in dynamically added CIDR, got %v", err)
	}
}

func TestBlacklistPlugin_Reload(t *testing.T) {
	p := NewBlacklistPlugin("192.168.1.100")
	defer p.Close()

	// Verify initial block
	sess := newMockSession(1, "192.168.1.100")
	if err := p.OnAccept(sess); !isErrBlock(err) {
		t.Fatalf("expected ErrBlock for initial IP, got %v", err)
	}

	// Reload with a completely different list
	p.Reload([]string{"10.0.0.0/8", "1.2.3.4"})

	// Old IP should now pass
	sess2 := newMockSession(2, "192.168.1.100")
	if err := p.OnAccept(sess2); err != nil {
		t.Fatalf("expected nil for old IP after reload, got %v", err)
	}

	// New CIDR should block
	sess3 := newMockSession(3, "10.5.5.5")
	if err := p.OnAccept(sess3); !isErrBlock(err) {
		t.Fatalf("expected ErrBlock for new CIDR after reload, got %v", err)
	}

	// New exact IP should block
	sess4 := newMockSession(4, "1.2.3.4")
	if err := p.OnAccept(sess4); !isErrBlock(err) {
		t.Fatalf("expected ErrBlock for new exact IP after reload, got %v", err)
	}
}

func TestBlacklistPlugin_OnMessagePassthrough(t *testing.T) {
	p := NewBlacklistPlugin("1.2.3.4")
	defer p.Close()

	sess := newMockSession(1, "1.2.3.4")
	data := []byte("hello")
	out, err := p.OnMessage(sess, data)
	if err != nil {
		t.Fatalf("OnMessage should not error, got %v", err)
	}
	if string(out) != string(data) {
		t.Fatalf("OnMessage should pass data through, got %q", out)
	}
}

func TestBlacklistPlugin_NamePriority(t *testing.T) {
	p := NewBlacklistPlugin()
	defer p.Close()

	if p.Name() != "blacklist" {
		t.Fatalf("expected name 'blacklist', got %q", p.Name())
	}
	if p.Priority() != 0 {
		t.Fatalf("expected priority 0, got %d", p.Priority())
	}
}

func TestBlacklistPlugin_IPv6Blocking(t *testing.T) {
	p := NewBlacklistPlugin("::1")
	defer p.Close()

	sess := &mockRawSession{
		id:         1,
		protocol:   1,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("::1"), Port: 12345},
		localAddr:  &net.TCPAddr{IP: net.ParseIP("::"), Port: 8080},
		state:      1,
		alive:      true,
		meta:       make(map[string]any),
	}
	if err := p.OnAccept(sess); !isErrBlock(err) {
		t.Fatalf("expected ErrBlock for IPv6 ::1, got %v", err)
	}
}

func TestBlacklistPlugin_TTLExpiry(t *testing.T) {
	p := NewBlacklistPlugin()
	defer p.Close()

	// Add with a very short TTL
	p.Add("9.8.7.6", 50*time.Millisecond)

	sess := newMockSession(1, "9.8.7.6")
	if err := p.OnAccept(sess); !isErrBlock(err) {
		t.Fatalf("expected ErrBlock before TTL expires, got %v", err)
	}

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	sess2 := newMockSession(2, "9.8.7.6")
	if err := p.OnAccept(sess2); err != nil {
		t.Fatalf("expected nil after TTL expires, got %v", err)
	}
}

func isErrBlock(err error) bool {
	return err != nil && err == errs.ErrBlock
}
