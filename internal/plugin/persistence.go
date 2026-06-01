package plugin

import (
	"fmt"
	"log"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/store"
)

type Persistence struct {
	core.BasePlugin
	store  store.Store
	bucket string
}

func NewPersistence(s store.Store, bucket string) *Persistence {
	if bucket == "" {
		bucket = "sessions"
	}
	return &Persistence{store: s, bucket: bucket}
}

func (p *Persistence) Name() string  { return "persistence" }
func (p *Persistence) Priority() int { return 90 }

func (p *Persistence) OnAccept(sess core.Session) error {
	if p.store == nil {
		return nil
	}
	value := []byte(fmt.Sprintf("accepted protocol=%s remote=%s at=%s", sess.Protocol(), sess.RemoteAddr(), time.Now().UTC().Format(time.RFC3339Nano)))
	p.store.Save(p.bucket, p.key(sess), value)
	return nil
}

func (p *Persistence) OnClose(sess core.Session) {
	if p.store == nil {
		return
	}
	value := []byte(fmt.Sprintf("closed protocol=%s remote=%s at=%s", sess.Protocol(), sess.RemoteAddr(), time.Now().UTC().Format(time.RFC3339Nano)))
	p.store.Save(p.bucket, p.key(sess), value)
}

func (p *Persistence) key(sess core.Session) string {
	return fmt.Sprintf("%s/%d", sess.Protocol(), sess.ID())
}

type PersistenceV2 struct {
	core.BasePlugin
	store      store.StoreV2
	bucket     string
	messageLog *store.MessageLog
}

func NewPersistenceV2(s store.StoreV2, bucket string) *PersistenceV2 {
	if bucket == "" {
		bucket = "sessions"
	}
	msgLog, err := store.NewMessageLog(s, bucket+"/messages")
	if err != nil {
		log.Printf("persistence-v2: failed to init message log: %v", err)
	}
	return &PersistenceV2{store: s, bucket: bucket, messageLog: msgLog}
}

func (p *PersistenceV2) Name() string  { return "persistence-v2" }
func (p *PersistenceV2) Priority() int { return 90 }

func (p *PersistenceV2) OnAccept(sess core.Session) error {
	if p.store == nil {
		return nil
	}
	value := []byte(fmt.Sprintf("accepted protocol=%s remote=%s at=%s", sess.Protocol(), sess.RemoteAddr(), time.Now().UTC().Format(time.RFC3339Nano)))
	if err := p.store.SaveV2(p.bucket, p.key(sess), value); err != nil {
		log.Printf("persistence-v2: save on accept: %v", err)
	}
	return nil
}

func (p *PersistenceV2) OnClose(sess core.Session) {
	if p.store == nil {
		return
	}
	value := []byte(fmt.Sprintf("closed protocol=%s remote=%s at=%s", sess.Protocol(), sess.RemoteAddr(), time.Now().UTC().Format(time.RFC3339Nano)))
	if err := p.store.SaveV2(p.bucket, p.key(sess), value); err != nil {
		log.Printf("persistence-v2: save on close: %v", err)
	}
}

func (p *PersistenceV2) OnMessage(sess core.Session, data []byte) ([]byte, error) {
	if p.messageLog != nil {
		if _, err := p.messageLog.Append(data); err != nil {
			log.Printf("persistence-v2: message log append: %v", err)
		}
	}
	return data, nil
}

func (p *PersistenceV2) MessageLog() *store.MessageLog { return p.messageLog }

func (p *PersistenceV2) key(sess core.Session) string {
	return fmt.Sprintf("%s/%d", sess.Protocol(), sess.ID())
}
