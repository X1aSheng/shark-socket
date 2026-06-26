package plugin

import (
	"fmt"
	"log"
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
}

// NewPersistence creates a persistence plugin backed by the given Store.
func NewPersistence(s store.Store, bucket string) *Persistence {
	if bucket == "" {
		bucket = "sessions"
	}
	var msgLog *store.MessageLog
	if s != nil {
		var err error
		msgLog, err = store.NewMessageLog(s, bucket+"/messages")
		if err != nil {
			log.Printf("persistence: failed to init message log: %v", err)
		}
	}
	return &Persistence{store: s, bucket: bucket, messageLog: msgLog}
}

func (p *Persistence) Name() string  { return "persistence" }
func (p *Persistence) Priority() int { return 90 }

func (p *Persistence) OnAccept(sess core.Session) error {
	if p.store == nil {
		return nil
	}
	value := []byte(fmt.Sprintf("accepted protocol=%s remote=%s at=%s", sess.Protocol(), sess.RemoteAddr(), time.Now().UTC().Format(time.RFC3339Nano)))
	if err := p.store.Save(p.bucket, p.key(sess), value); err != nil {
		log.Printf("persistence: save on accept: %v", err)
	}
	return nil
}

func (p *Persistence) OnClose(sess core.Session) {
	if p.store == nil {
		return
	}
	value := []byte(fmt.Sprintf("closed protocol=%s remote=%s at=%s", sess.Protocol(), sess.RemoteAddr(), time.Now().UTC().Format(time.RFC3339Nano)))
	if err := p.store.Save(p.bucket, p.key(sess), value); err != nil {
		log.Printf("persistence: save on close: %v", err)
	}
}

func (p *Persistence) OnMessage(sess core.Session, data []byte) ([]byte, error) {
	if p.messageLog != nil {
		if _, err := p.messageLog.Append(data); err != nil {
			log.Printf("persistence: message log append: %v", err)
		}
	}
	return data, nil
}

func (p *Persistence) MessageLog() *store.MessageLog { return p.messageLog }

func (p *Persistence) key(sess core.Session) string {
	return fmt.Sprintf("%s/%d", sess.Protocol(), sess.ID())
}
