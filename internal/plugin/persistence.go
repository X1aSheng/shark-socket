package plugin

import (
	"fmt"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/store"
)

// Persistence persists session lifecycle events to a Store.
type Persistence struct {
	core.BasePlugin
	store      store.Store
	bucket     string
	messageLog *store.MessageLog
	loggerMu   sync.RWMutex
	logger     core.Logger
}

// NewPersistence creates a persistence plugin backed by the given Store.
func NewPersistence(s store.Store, bucket string) *Persistence {
	if bucket == "" {
		bucket = "sessions"
	}
	p := &Persistence{store: s, bucket: bucket, logger: core.NopLogger()}
	if s != nil {
		var err error
		p.messageLog, err = store.NewMessageLog(s, bucket+"/messages")
		if err != nil {
			p.logger.Error("persistence: failed to init message log", "error", err)
		}
	}
	return p
}

// SetLogger sets the logger used for operational messages.
// The write is locked so it is safe to call while the plugin is active.
func (p *Persistence) SetLogger(logger core.Logger) {
	if logger == nil {
		return
	}
	p.loggerMu.Lock()
	p.logger = logger
	p.loggerMu.Unlock()
}

func (p *Persistence) Name() string  { return "persistence" }
func (p *Persistence) Priority() int { return 90 }

// loggerRef returns the current logger under a read lock.
func (p *Persistence) loggerRef() core.Logger {
	p.loggerMu.RLock()
	l := p.logger
	p.loggerMu.RUnlock()
	return l
}

func (p *Persistence) OnAccept(sess core.Session) error {
	if p.store == nil {
		return nil
	}
	value := []byte(fmt.Sprintf("accepted protocol=%s remote=%s at=%s", sess.Protocol(), sess.RemoteAddr(), time.Now().UTC().Format(time.RFC3339Nano)))
	if err := p.store.Save(p.bucket, p.key(sess), value); err != nil {
		p.loggerRef().Error("persistence: save on accept", "error", err)
	}
	return nil
}

func (p *Persistence) OnClose(sess core.Session) {
	if p.store == nil {
		return
	}
	value := []byte(fmt.Sprintf("closed protocol=%s remote=%s at=%s", sess.Protocol(), sess.RemoteAddr(), time.Now().UTC().Format(time.RFC3339Nano)))
	if err := p.store.Save(p.bucket, p.key(sess), value); err != nil {
		p.loggerRef().Error("persistence: save on close", "error", err)
	}
}

func (p *Persistence) OnMessage(sess core.Session, data []byte) ([]byte, error) {
	if p.messageLog != nil {
		if _, err := p.messageLog.Append(data); err != nil {
			p.loggerRef().Error("persistence: message log append", "error", err)
		}
	}
	return data, nil
}

func (p *Persistence) MessageLog() *store.MessageLog { return p.messageLog }

func (p *Persistence) key(sess core.Session) string {
	return fmt.Sprintf("%s/%d", sess.Protocol(), sess.ID())
}
