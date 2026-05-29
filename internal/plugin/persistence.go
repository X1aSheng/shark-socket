package plugin

import (
	"fmt"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/infra/store"
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
