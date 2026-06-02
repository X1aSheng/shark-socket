package plugin

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/infra/store"
)

// Test all plugin Name and Priority methods for coverage
func TestBlacklistNamePriority(t *testing.T) {
	b := NewBlacklist("192.168.1.1")
	if b.Name() != "blacklist" {
		t.Fatalf("Name = %s, want blacklist", b.Name())
	}
	_ = b.Priority() // just for coverage
}

func TestRateLimitNamePriority(t *testing.T) {
	r := NewRateLimit(10, time.Second)
	if r.Name() != "ratelimit" {
		t.Fatalf("Name = %s, want ratelimit", r.Name())
	}
	if r.Priority() == 0 {
		t.Fatal("Priority should not be 0")
	}
}

func TestAutoBanNamePriority(t *testing.T) {
	a := NewAutoBan(3)
	if a.Name() != "autoban" {
		t.Fatalf("Name = %s, want autoban", a.Name())
	}
	if a.Priority() == 0 {
		t.Fatal("Priority should not be 0")
	}
}

func TestHeartbeatNamePriority(t *testing.T) {
	h := NewHeartbeat(nil, 30)
	if h.Name() != "heartbeat" {
		t.Fatalf("Name = %s, want heartbeat", h.Name())
	}
	if h.Priority() == 0 {
		t.Fatal("Priority should not be 0")
	}
}

func TestPersistenceNamePriority(t *testing.T) {
	p := NewPersistence(nil, "bucket")
	if p.Name() != "persistence" {
		t.Fatalf("Name = %s, want persistence", p.Name())
	}
	if p.Priority() == 0 {
		t.Fatal("Priority should not be 0")
	}
}

func TestPersistenceV2NamePriority(t *testing.T) {
	s := store.NewMemory()
	p := NewPersistenceV2(s, "bucket")
	if p.Name() != "persistence-v2" {
		t.Fatalf("Name = %s, want persistence-v2", p.Name())
	}
	if p.Priority() == 0 {
		t.Fatal("Priority should not be 0")
	}
}

func TestClusterNamePriority(t *testing.T) {
	c := NewCluster("node-1", nil, nil)
	if c.Name() != "cluster" {
		t.Fatalf("Name = %s, want cluster", c.Name())
	}
	if c.Priority() == 0 {
		t.Fatal("Priority should not be 0")
	}
}

func TestClusterWithTopic(t *testing.T) {
	c := NewCluster("node-2", nil, nil)
	c.WithTopic("custom/topic")
}
