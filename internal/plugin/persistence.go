package plugin

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/yourname/shark-socket/internal/infra/circuitbreaker"
	"github.com/yourname/shark-socket/internal/infra/store"
	"github.com/yourname/shark-socket/internal/types"
)

type persistEntry struct {
	SessionID uint64
	Data      []byte
	Timestamp time.Time
}

// PersistencePlugin asynchronously persists session state to a Store.
type PersistencePlugin struct {
	store        store.Store
	cb           *circuitbreaker.CircuitBreaker
	writeCh      chan *persistEntry
	batchSize    int
	flushInterval time.Duration
	stopCh       chan struct{}
}

// PersistenceOption configures PersistencePlugin.
type PersistenceOption func(*PersistencePlugin)

// WithBatchSize sets the batch size for flush.
func WithBatchSize(n int) PersistenceOption {
	return func(p *PersistencePlugin) { p.batchSize = n }
}

// WithFlushInterval sets the flush interval.
func WithFlushInterval(d time.Duration) PersistenceOption {
	return func(p *PersistencePlugin) { p.flushInterval = d }
}

// NewPersistencePlugin creates a new persistence plugin.
func NewPersistencePlugin(s store.Store, opts ...PersistenceOption) *PersistencePlugin {
	p := &PersistencePlugin{
		store:         s,
		cb:            circuitbreaker.New(5, 30*time.Second),
		writeCh:       make(chan *persistEntry, 1024),
		batchSize:     100,
		flushInterval: 500 * time.Millisecond,
		stopCh:        make(chan struct{}),
	}
	for _, opt := range opts {
		opt(p)
	}
	go p.batchWriter()
	return p
}

func (p *PersistencePlugin) Name() string  { return "persistence" }
func (p *PersistencePlugin) Priority() int { return 50 }

func (p *PersistencePlugin) OnAccept(sess types.RawSession) error {
	return p.cb.Do(func() error {
		val, e := p.store.Load(nil, sessionKey(sess.ID()))
		if e != nil {
			return e
		}
		sess.SetMeta("history", val)
		return nil
	})
}

func (p *PersistencePlugin) OnMessage(sess types.RawSession, data []byte) ([]byte, error) {
	entry := &persistEntry{
		SessionID: sess.ID(),
		Data:      data,
		Timestamp: time.Now(),
	}
	select {
	case p.writeCh <- entry:
	default:
		// channel full, drop silently to avoid blocking
	}
	return data, nil
}

func (p *PersistencePlugin) OnClose(sess types.RawSession) {
	data, _ := json.Marshal(map[string]any{
		"session_id": sess.ID(),
		"closed_at":  time.Now(),
	})
	_ = p.cb.Do(func() error {
		return p.store.Save(nil, sessionKey(sess.ID()), data)
	})
}

func (p *PersistencePlugin) batchWriter() {
	batch := make([]*persistEntry, 0, 100)
	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case entry := <-p.writeCh:
			batch = append(batch, entry)
			if len(batch) >= p.batchSize {
				p.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				p.flush(batch)
				batch = batch[:0]
			}
		case <-p.stopCh:
			// final flush
			for len(p.writeCh) > 0 {
				entry := <-p.writeCh
				batch = append(batch, entry)
			}
			if len(batch) > 0 {
				p.flush(batch)
			}
			return
		}
	}
}

func (p *PersistencePlugin) flush(batch []*persistEntry) {
	for _, entry := range batch {
		data, _ := json.Marshal(entry)
		_ = p.cb.Do(func() error {
			return p.store.Save(nil, sessionKey(entry.SessionID), data)
		})
	}
}

func sessionKey(id uint64) string {
	return "session:" + strconv.FormatUint(id, 10)
}

// Close stops the batch writer.
func (p *PersistencePlugin) Close() {
	close(p.stopCh)
}

// IsCircuitOpen checks if the persistence circuit breaker is open.
func (p *PersistencePlugin) IsCircuitOpen() bool {
	return p.cb.State() == circuitbreaker.Open
}
