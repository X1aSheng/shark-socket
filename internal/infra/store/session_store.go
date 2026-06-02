package store

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// SessionSnapshot holds serializable session state for recovery.
type SessionSnapshot struct {
	ID       uint64            `json:"id"`
	Protocol string            `json:"protocol"`
	Remote   string            `json:"remote"`
	Local    string            `json:"local"`
	State    string            `json:"state"`
	Meta     map[string]string `json:"meta"`
}

// SessionStore persists session snapshots for restart recovery.
type SessionStore struct {
	store  StoreV2
	bucket string
}

func NewSessionStore(store StoreV2, bucket string) *SessionStore {
	if bucket == "" {
		bucket = "snapshots"
	}
	return &SessionStore{store: store, bucket: bucket}
}

func (s *SessionStore) SaveSnapshot(snap SessionSnapshot) error {
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("session_store: marshal: %w", err)
	}
	return s.store.SaveV2(s.bucket, fmt.Sprintf("%d", snap.ID), data)
}

func (s *SessionStore) LoadSnapshot(id uint64) (SessionSnapshot, bool, error) {
	data, ok, err := s.store.LoadV2(s.bucket, fmt.Sprintf("%d", id))
	if err != nil || !ok {
		return SessionSnapshot{}, ok, err
	}
	var snap SessionSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return SessionSnapshot{}, false, fmt.Errorf("session_store: unmarshal: %w", err)
	}
	return snap, true, nil
}

func (s *SessionStore) ListSnapshots() ([]SessionSnapshot, error) {
	keys, err := s.store.List(s.bucket)
	if err != nil {
		return nil, err
	}
	var snaps []SessionSnapshot
	for _, key := range keys {
		id, ok := parseUint64(key)
		if !ok {
			continue // skip invalid snapshot keys
		}
		snap, ok, err := s.LoadSnapshot(id)
		if err != nil {
			return nil, err
		}
		if ok {
			snaps = append(snaps, snap)
		}
	}
	return snaps, nil
}

func (s *SessionStore) DeleteSnapshot(id uint64) error {
	return s.store.DeleteV2(s.bucket, fmt.Sprintf("%d", id))
}

func parseUint64(s string) (uint64, bool) {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
