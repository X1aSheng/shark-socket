package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBoltStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bolt")
	bs, err := NewBoltStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()

	if err := bs.SaveV2("sessions", "abc", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	v, ok, err := bs.LoadV2("sessions", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || string(v) != "hello" {
		t.Fatalf("LoadV2: ok=%v val=%s", ok, string(v))
	}
	if err := bs.DeleteV2("sessions", "abc"); err != nil {
		t.Fatal(err)
	}
	_, ok, err = bs.LoadV2("sessions", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected key to be deleted")
	}
}

func TestBoltStoreList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bolt")
	bs, err := NewBoltStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()

	bs.SaveV2("data", "k1", nil)
	bs.SaveV2("data", "k2", nil)
	bs.SaveV2("data", "k3", nil)
	keys, err := bs.List("data")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 {
		t.Fatalf("want 3 keys, got %d", len(keys))
	}
}

func TestBoltStoreClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bolt")
	bs, err := NewBoltStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bs.SaveV2("x", "y", nil); err == nil {
		t.Fatal("expected error after close")
	}
}

func TestBoltStoreReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bolt")
	bs, err := NewBoltStore(path)
	if err != nil {
		t.Fatal(err)
	}
	bs.SaveV2("bucket", "key", []byte("persist"))
	bs.Close()

	bs2, err := NewBoltStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer bs2.Close()
	v, ok, err := bs2.LoadV2("bucket", "key")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || string(v) != "persist" {
		t.Fatalf("reopen: ok=%v val=%s", ok, string(v))
	}
}

func TestBoltStoreMissingDirCreated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "subdir")
	path := filepath.Join(dir, "test.bolt")
	bs, err := NewBoltStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("directory not created: %v", err)
	}
}

func TestMemoryStoreV2(t *testing.T) {
	m := NewMemory()
	if err := m.SaveV2("b", "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	v, ok, err := m.LoadV2("b", "k")
	if err != nil || !ok || string(v) != "v" {
		t.Fatalf("LoadV2: ok=%v val=%s err=%v", ok, string(v), err)
	}
	keys, err := m.List("b")
	if err != nil || len(keys) != 1 {
		t.Fatalf("List: %v err=%v", keys, err)
	}
}

func TestBoltStoreLegacySaveLoadDelete(t *testing.T) {
	dir := t.TempDir()
	bs, err := NewBoltStore(filepath.Join(dir, "test.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()

	// Legacy Save
	bs.Save("legacy", "k1", []byte("v1"))
	// Legacy Load
	val, ok := bs.Load("legacy", "k1")
	if !ok || string(val) != "v1" {
		t.Fatalf("Load: ok=%v val=%s", ok, string(val))
	}
	// Legacy Delete
	bs.Delete("legacy", "k1")
	val, ok = bs.Load("legacy", "k1")
	if ok {
		t.Fatal("expected key to be deleted")
	}
}

func TestBoltStoreDeleteBatch(t *testing.T) {
	dir := t.TempDir()
	bs, err := NewBoltStore(filepath.Join(dir, "test.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()

	keys := []string{"a", "b", "c"}
	for _, k := range keys {
		if err := bs.SaveV2("batch", k, []byte("data")); err != nil {
			t.Fatal(err)
		}
	}
	// Verify 3 keys exist
	list, err := bs.List("batch")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("before delete: want 3 keys, got %d", len(list))
	}

	// DeleteBatch
	if err := bs.DeleteBatch("batch", []string{"a", "c"}); err != nil {
		t.Fatal(err)
	}

	list, err = bs.List("batch")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0] != "b" {
		t.Fatalf("after DeleteBatch: want [b], got %v", list)
	}
}

func TestBoltStoreNewWithMissingDir(t *testing.T) {
	// NewBoltStore with non-existent parent dir - should create it
	path := filepath.Join(t.TempDir(), "a", "b", "test.bolt")
	bs, err := NewBoltStore(path)
	if err != nil {
		t.Fatal(err)
	}
	bs.Close()
}

func TestBoltStoreNewWithInvalidPath(t *testing.T) {
	// Invalid path should fail
	_, err := NewBoltStore("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}
