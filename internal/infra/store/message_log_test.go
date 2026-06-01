package store

import (
	"testing"
)

func TestMessageLogAppendAndReplay(t *testing.T) {
	mem := NewMemory()
	log, err := NewMessageLog(mem, "messages")
	if err != nil {
		t.Fatal(err)
	}
	seq1, err := log.Append([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	seq2, err := log.Append([]byte("world"))
	if err != nil {
		t.Fatal(err)
	}
	if seq1 >= seq2 {
		t.Fatalf("seq1=%d >= seq2=%d", seq1, seq2)
	}
	replayed := make(map[uint64]string)
	err = log.Replay(func(seq uint64, data []byte) error {
		replayed[seq] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 2 {
		t.Fatalf("want 2 replays, got %d", len(replayed))
	}
	if replayed[seq1] != "hello" || replayed[seq2] != "world" {
		t.Fatalf("replayed data mismatch: %v", replayed)
	}
}

func TestMessageLogPrune(t *testing.T) {
	mem := NewMemory()
	log, _ := NewMessageLog(mem, "messages")
	log.Append([]byte("a"))
	log.Append([]byte("b"))
	log.Append([]byte("c"))
	if err := log.Prune(2); err != nil {
		t.Fatal(err)
	}
	n, _ := log.Len()
	if n != 2 {
		t.Fatalf("Len after prune = %d, want 2", n)
	}
}

func TestMessageLogResume(t *testing.T) {
	mem := NewMemory()
	log1, _ := NewMessageLog(mem, "messages")
	log1.Append([]byte("a"))
	log1.Append([]byte("b"))

	log2, _ := NewMessageLog(mem, "messages")
	seq, err := log2.Append([]byte("c"))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := log2.Len()
	if n != 3 {
		t.Fatalf("Len = %d, want 3", n)
	}
	if seq < 2 {
		t.Fatalf("seq after resume = %d, want >= 2", seq)
	}
}

func TestMessageLogLen(t *testing.T) {
	mem := NewMemory()
	log, _ := NewMessageLog(mem, "messages")
	if n, _ := log.Len(); n != 0 {
		t.Fatalf("empty Len = %d", n)
	}
	log.Append(nil)
	log.Append(nil)
	if n, _ := log.Len(); n != 2 {
		t.Fatalf("Len = %d, want 2", n)
	}
}

func TestMessageLogOnBolt(t *testing.T) {
	dir := t.TempDir()
	bs, err := NewBoltStore(dir + "/msglog.bolt")
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()

	log, err := NewMessageLog(bs, "messages")
	if err != nil {
		t.Fatal(err)
	}
	seq, err := log.Append([]byte("persisted"))
	if err != nil {
		t.Fatal(err)
	}

	bs.Close()
	bs2, _ := NewBoltStore(dir + "/msglog.bolt")
	defer bs2.Close()
	log2, err := NewMessageLog(bs2, "messages")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	log2.Replay(func(s uint64, data []byte) error {
		if s == seq && string(data) == "persisted" {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("persisted message not replayed")
	}
}
