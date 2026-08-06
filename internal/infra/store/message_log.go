package store

import (
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
)

// MessageLog is a durable append-only message log backed by Store.
// Messages are stored under a bucket with auto-incrementing sequence numbers.
type MessageLog struct {
	store  Store
	bucket string
	mu     sync.Mutex
	next   uint64
}

// NewMessageLog creates a message log in the given bucket. On open, it
// scans existing keys to resume the sequence counter.
func NewMessageLog(store Store, bucket string) (*MessageLog, error) {
	keys, err := store.List(bucket)
	if err != nil {
		return nil, fmt.Errorf("message_log: list %s: %w", bucket, err)
	}
	var maxSeq uint64
	for _, k := range keys {
		if len(k) >= 8 {
			seq := binary.BigEndian.Uint64([]byte(k)[:8])
			if seq > maxSeq {
				maxSeq = seq
			}
		}
	}
	return &MessageLog{store: store, bucket: bucket, next: maxSeq + 1}, nil
}

// Append writes a message to the log and returns its sequence number.
func (m *MessageLog) Append(data []byte) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seq := m.next
	key := seqKey(seq)
	if err := m.store.Save(m.bucket, key, data); err != nil {
		return 0, fmt.Errorf("message_log: append: %w", err)
	}
	m.next++
	return seq, nil
}

// Replay calls fn for every message in the log, in sequence order.
// Safe for concurrent use with Append; sees a point-in-time snapshot.
func (m *MessageLog) Replay(fn func(seq uint64, data []byte) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys, err := m.store.List(m.bucket)
	if err != nil {
		return err
	}
	sort.Strings(keys)
	for _, key := range keys {
		// Keys shorter than the 8-byte sequence prefix (written by another
		// bucket or corrupted data) must be skipped, not sliced out of range.
		if len(key) < 8 {
			continue
		}
		val, ok, err := m.store.Load(m.bucket, key)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		seq := binary.BigEndian.Uint64([]byte(key)[:8])
		if err := fn(seq, val); err != nil {
			return err
		}
	}
	return nil
}

// Prune removes messages up to (but not including) the given sequence number.
// Safe for concurrent use with Append.
func (m *MessageLog) Prune(beforeSeq uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys, err := m.store.List(m.bucket)
	if err != nil {
		return err
	}
	var toDelete []string
	for _, key := range keys {
		if len(key) < 8 {
			continue
		}
		seq := binary.BigEndian.Uint64([]byte(key)[:8])
		if seq < beforeSeq {
			toDelete = append(toDelete, key)
		}
	}
	if len(toDelete) == 0 {
		return nil
	}
	// Use batch delete if the store supports it
	if bd, ok := m.store.(BulkDeleter); ok {
		return bd.DeleteBatch(m.bucket, toDelete)
	}
	for _, key := range toDelete {
		if err := m.store.Delete(m.bucket, key); err != nil {
			return err
		}
	}
	return nil
}

// Len returns the total number of messages in the log.
func (m *MessageLog) Len() (int, error) {
	keys, err := m.store.List(m.bucket)
	if err != nil {
		return 0, err
	}
	return len(keys), nil
}

func seqKey(seq uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, seq)
	return string(buf)
}
