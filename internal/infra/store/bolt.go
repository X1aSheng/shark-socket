package store

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/X1aSheng/shark-socket/internal/core"
	bolt "go.etcd.io/bbolt"
)

// BoltStore implements StoreV2 backed by a BoltDB file.
type BoltStore struct {
	mu     sync.RWMutex
	db     *bolt.DB
	closed bool
}

// NewBoltStore opens or creates a BoltDB database at the given path.
func NewBoltStore(path string) (*BoltStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("bolt: mkdir: %w", err)
	}
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("bolt: open: %w", err)
	}
	return &BoltStore{db: db}, nil
}

func (b *BoltStore) isClosed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.closed
}

func (b *BoltStore) Save(bucket, key string, value []byte) {
	if err := b.SaveV2(bucket, key, value); err != nil {
		log.Printf("bolt: save error in bucket %s: %v", bucket, err)
	}
}

func (b *BoltStore) SaveV2(bucket, key string, value []byte) error {
	if b.isClosed() {
		return core.ErrClosed
	}
	return b.db.Update(func(tx *bolt.Tx) error {
		bk, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return fmt.Errorf("bolt: save bucket %s: %w", bucket, err)
		}
		return bk.Put([]byte(key), value)
	})
}

func (b *BoltStore) Load(bucket, key string) ([]byte, bool) {
	v, ok, _ := b.LoadV2(bucket, key)
	return v, ok
}

func (b *BoltStore) LoadV2(bucket, key string) ([]byte, bool, error) {
	if b.isClosed() {
		return nil, false, core.ErrClosed
	}
	var val []byte
	err := b.db.View(func(tx *bolt.Tx) error {
		bk := tx.Bucket([]byte(bucket))
		if bk == nil {
			return nil
		}
		v := bk.Get([]byte(key))
		if v != nil {
			val = make([]byte, len(v))
			copy(val, v)
		}
		return nil
	})
	return val, val != nil, err
}

func (b *BoltStore) Delete(bucket, key string) {
	if err := b.DeleteV2(bucket, key); err != nil {
		log.Printf("bolt: delete error in bucket %s: %v", bucket, err)
	}
}

func (b *BoltStore) DeleteV2(bucket, key string) error {
	if b.isClosed() {
		return core.ErrClosed
	}
	return b.db.Update(func(tx *bolt.Tx) error {
		bk := tx.Bucket([]byte(bucket))
		if bk == nil {
			return nil
		}
		return bk.Delete([]byte(key))
	})
}

func (b *BoltStore) List(bucket string) ([]string, error) {
	if b.isClosed() {
		return nil, core.ErrClosed
	}
	var keys []string
	err := b.db.View(func(tx *bolt.Tx) error {
		bk := tx.Bucket([]byte(bucket))
		if bk == nil {
			return nil
		}
		return bk.ForEach(func(k, _ []byte) error {
			keys = append(keys, string(k))
			return nil
		})
	})
	return keys, err
}

// DeleteBatch deletes multiple keys within a single transaction.
func (b *BoltStore) DeleteBatch(bucket string, keys []string) error {
	if b.isClosed() {
		return core.ErrClosed
	}
	return b.db.Update(func(tx *bolt.Tx) error {
		bk := tx.Bucket([]byte(bucket))
		if bk == nil {
			return nil
		}
		for _, key := range keys {
			if err := bk.Delete([]byte(key)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (b *BoltStore) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return b.db.Close()
}

var (
	_ StoreV2     = (*BoltStore)(nil)
	_ BulkDeleter = (*BoltStore)(nil)
)
