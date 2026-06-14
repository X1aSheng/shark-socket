package store

import (
	"testing"
)

func TestSessionStoreSaveAndLoad(t *testing.T) {
	mem := NewMemory()
	ss := NewSessionStore(mem, "snapshots")

	snap := SessionSnapshot{
		ID:       1,
		Protocol: "tcp",
		Remote:   "192.168.1.1:1234",
		Local:    "0.0.0.0:18000",
		State:    "active",
		Meta:     map[string]string{"node": "east"},
	}
	if err := ss.SaveSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	loaded, ok, err := ss.LoadSnapshot(1)
	if err != nil || !ok {
		t.Fatalf("LoadSnapshot: ok=%v err=%v", ok, err)
	}
	if loaded.ID != 1 || loaded.Protocol != "tcp" || loaded.Remote != "192.168.1.1:1234" {
		t.Fatalf("snapshot mismatch: %#v", loaded)
	}
	if loaded.Meta["node"] != "east" {
		t.Fatalf("meta mismatch: %v", loaded.Meta)
	}
}

func TestSessionStoreLoadMissing(t *testing.T) {
	mem := NewMemory()
	ss := NewSessionStore(mem, "snapshots")
	_, ok, err := ss.LoadSnapshot(999)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected not found")
	}
}

func TestSessionStoreList(t *testing.T) {
	mem := NewMemory()
	ss := NewSessionStore(mem, "snapshots")
	ss.SaveSnapshot(SessionSnapshot{ID: 1, Protocol: "tcp"})
	ss.SaveSnapshot(SessionSnapshot{ID: 2, Protocol: "udp"})

	snaps, err := ss.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("want 2 snapshots, got %d", len(snaps))
	}
}

func TestSessionStoreDelete(t *testing.T) {
	mem := NewMemory()
	ss := NewSessionStore(mem, "snapshots")
	ss.SaveSnapshot(SessionSnapshot{ID: 1})
	if err := ss.DeleteSnapshot(1); err != nil {
		t.Fatal(err)
	}
	_, ok, _ := ss.LoadSnapshot(1)
	if ok {
		t.Fatal("expected deleted")
	}
}

func TestSessionStoreOnBolt(t *testing.T) {
	dir := t.TempDir()
	bs, err := NewBoltStore(dir + "/session.bolt")
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()

	ss := NewSessionStore(bs, "snapshots")
	ss.SaveSnapshot(SessionSnapshot{ID: 42, Protocol: "tcp", Remote: "10.0.0.1:9000"})

	bs.Close()
	bs2, _ := NewBoltStore(dir + "/session.bolt")
	defer bs2.Close()
	ss2 := NewSessionStore(bs2, "snapshots")

	snap, ok, err := ss2.LoadSnapshot(42)
	if err != nil || !ok {
		t.Fatalf("Load after reopen: ok=%v err=%v", ok, err)
	}
	if snap.ID != 42 || snap.Remote != "10.0.0.1:9000" {
		t.Fatalf("snapshot mismatch: %#v", snap)
	}
}

func TestNewSessionStoreWithMemoryStore(t *testing.T) {
	m := NewMemory()
	s := NewSessionStore(m, "custom-snapshots")
	if s == nil {
		t.Fatal("session store should not be nil")
	}
}

func TestNewSessionStoreDefaultBucket(t *testing.T) {
	m := NewMemory()
	s := NewSessionStore(m, "")
	if s == nil {
		t.Fatal("session store should not be nil")
	}
}
