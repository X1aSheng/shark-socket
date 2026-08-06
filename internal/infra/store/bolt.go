package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/X1aSheng/shark-socket/internal/core"
	bolt "go.etcd.io/bbolt"
)

// BoltStore implements Store backed by a BoltDB file.
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

// withLock runs fn while holding the read lock and after confirming the store
// is not closed. Holding the lock for the whole operation closes the TOCTOU
// window where a concurrent Close() could shut the DB between the closed check
// and the DB access (which would surface bolt.ErrDatabaseNotOpen instead of
// core.ErrClosed).
func (b *BoltStore) withLock(fn func(db *bolt.DB) error) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return core.ErrClosed
	}
	return fn(b.db)
}

func (b *BoltStore) Save(bucket, key string, value []byte) error {
	return b.withLock(func(db *bolt.DB) error {
		return db.Update(func(tx *bolt.Tx) error {
			bk, err := tx.CreateBucketIfNotExists([]byte(bucket))
			if err != nil {
				return fmt.Errorf("bolt: save bucket %s: %w", bucket, err)
			}
			return bk.Put([]byte(key), value)
		})
	})
}

func (b *BoltStore) Load(bucket, key string) ([]byte, bool, error) {
	var val []byte
	err := b.withLock(func(db *bolt.DB) error {
		return db.View(func(tx *bolt.Tx) error {
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
	})
	return val, val != nil, err
}

func (b *BoltStore) Delete(bucket, key string) error {
	return b.withLock(func(db *bolt.DB) error {
		return db.Update(func(tx *bolt.Tx) error {
			bk := tx.Bucket([]byte(bucket))
			if bk == nil {
				return nil
			}
			return bk.Delete([]byte(key))
		})
	})
}

func (b *BoltStore) List(bucket string) ([]string, error) {
	var keys []string
	err := b.withLock(func(db *bolt.DB) error {
		return db.View(func(tx *bolt.Tx) error {
			bk := tx.Bucket([]byte(bucket))
			if bk == nil {
				return nil
			}
			return bk.ForEach(func(k, _ []byte) error {
				keys = append(keys, string(k))
				return nil
			})
		})
	})
	return keys, err
}

// DeleteBatch deletes multiple keys within a single transaction.
func (b *BoltStore) DeleteBatch(bucket string, keys []string) error {
	return b.withLock(func(db *bolt.DB) error {
		return db.Update(func(tx *bolt.Tx) error {
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
	})
}

func (b *BoltStore) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return b.db.Close()
}

var (
	_ Store       = (*BoltStore)(nil)
	_ BulkDeleter = (*BoltStore)(nil)
)
