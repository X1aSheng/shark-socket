package coap

import (
	"sync"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// Observer represents a client subscribed to resource change notifications.
type Observer struct {
	Token    []byte
	Remote   string
	Resource string
	seq      uint32
	mu       sync.Mutex
}

// NextSeq returns and increments the observe sequence number.
func (o *Observer) NextSeq() uint32 {
	o.mu.Lock()
	v := o.seq
	o.seq++
	o.mu.Unlock()
	return v
}

// ObserverRegistry manages observe subscriptions per resource path.
type ObserverRegistry struct {
	mu    sync.RWMutex
	subs  map[string]map[string]*Observer
}

// NewObserverRegistry creates a new observer registry.
func NewObserverRegistry() *ObserverRegistry {
	return &ObserverRegistry{
		subs: make(map[string]map[string]*Observer),
	}
}

// Register subscribes an observer. The key is a unique combination of token + remote.
func (r *ObserverRegistry) Register(resource, remote string, token []byte) *Observer {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := observerKey(remote, token)
	if r.subs[resource] == nil {
		r.subs[resource] = make(map[string]*Observer)
	}
	obs := &Observer{Token: append([]byte(nil), token...), Remote: remote, Resource: resource}
	r.subs[resource][key] = obs
	return obs
}

// Remove unsubscribes an observer.
func (r *ObserverRegistry) Remove(resource, remote string, token []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeLocked(resource, remote, token)
}

func (r *ObserverRegistry) removeLocked(resource, remote string, token []byte) {
	key := observerKey(remote, token)
	if subs, ok := r.subs[resource]; ok {
		delete(subs, key)
		if len(subs) == 0 {
			delete(r.subs, resource)
		}
	}
}

// RemoveBySession removes all observers for a given remote address.
func (r *ObserverRegistry) RemoveBySession(remote string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for resource, subs := range r.subs {
		for key, obs := range subs {
			if obs.Remote == remote {
				delete(subs, key)
			}
		}
		if len(subs) == 0 {
			delete(r.subs, resource)
		}
	}
}

// Notify returns all observers for a resource path.
func (r *ObserverRegistry) Notify(resource string) []*Observer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	subs := r.subs[resource]
	result := make([]*Observer, 0, len(subs))
	for _, obs := range subs {
		result = append(result, obs)
	}
	return result
}

func observerKey(remote string, token []byte) string {
	return remote + "/" + string(token)
}

// SendObserveNotification sends an observe notification to the given session.
func SendObserveNotification(sess core.Session, msgID uint16, token []byte, seq uint32, payload []byte) error {
	seqBuf := []byte{byte(seq >> 24), byte(seq >> 16), byte(seq >> 8), byte(seq)}
	notify := Message{
		Type:      TypeCON,
		Code:      CodeContent,
		MessageID: msgID,
		Token:     token,
		Options:   map[uint16][]byte{ObserveOption: seqBuf},
		Payload:   payload,
	}
	data, err := notify.Marshal()
	if err != nil {
		return err
	}
	return sess.Send(data)
}
