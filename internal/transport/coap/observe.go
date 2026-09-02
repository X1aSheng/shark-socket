package coap

import (
	"sync"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// Subscription limits bound the observer registry so a single peer (or a UDP
// source-address flood) cannot grow it without limit. RFC 7641 relations are
// cheap individually, but each one fans notifications out (one CON datagram
// plus a retransmission-table entry per notification), so both a per-remote
// cap and a global cap are enforced at Register time.
const (
	maxObserversPerRemote = 64
	maxObserversTotal     = 4096
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
	mu          sync.RWMutex
	subs        map[string]map[string]*Observer // resource -> (remote+token) -> observer
	remoteCount map[string]int                  // number of relations per remote
	total       int
}

// NewObserverRegistry creates a new observer registry.
func NewObserverRegistry() *ObserverRegistry {
	return &ObserverRegistry{
		subs:        make(map[string]map[string]*Observer),
		remoteCount: make(map[string]int),
	}
}

// Register subscribes or refreshes an observer. The relation key is
// (remote, token); per RFC 7641 §3.6 a new GET+Observe for the same resource
// from the same client replaces the previous relation even when the token
// changes, so a peer cannot accumulate duplicate relations by rotating
// tokens. Returns nil when a subscription limit would be exceeded (the
// caller then answers the request without establishing an observe relation).
func (r *ObserverRegistry) Register(resource, remote string, token []byte) *Observer {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.subs[resource] == nil {
		r.subs[resource] = make(map[string]*Observer)
	}
	key := observerKey(remote, token)
	// Same (resource, remote, token) relation re-issued: keep it as-is
	// (sequence continuity, no double counting).
	if obs, ok := r.subs[resource][key]; ok {
		return obs
	}
	// Replace a previous relation of this client for the same resource even
	// if the token changed (RFC 7641 §3.6): the old relation must not linger.
	// A replacement is count-neutral and is never rejected by the caps below.
	replacing := false
	for oldKey, obs := range r.subs[resource] {
		if obs.Remote == remote {
			delete(r.subs[resource], oldKey)
			r.total--
			if r.remoteCount[remote] > 0 {
				r.remoteCount[remote]--
			}
			replacing = true
			break
		}
	}
	if !replacing {
		// Enforce caps only for genuinely new relations.
		if r.remoteCount[remote] >= maxObserversPerRemote || r.total >= maxObserversTotal {
			return nil
		}
	}
	if r.subs[resource] == nil {
		r.subs[resource] = make(map[string]*Observer)
	}
	obs := &Observer{Token: append([]byte(nil), token...), Remote: remote, Resource: resource}
	r.subs[resource][key] = obs
	r.remoteCount[remote]++
	r.total++
	return obs
}

// Remove unsubscribes an observer.
func (r *ObserverRegistry) Remove(resource, remote string, token []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeLocked(resource, remote, token)
}

// RemoveByToken unsubscribes the observer relation(s) identified by
// (remote, token) regardless of resource. Used when a peer rejects a CON
// notification with an RST (RFC 7641 §4.4) or after retransmission is given
// up: the notification token identifies the relation to cancel.
func (r *ObserverRegistry) RemoveByToken(remote string, token []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := observerKey(remote, token)
	for resource, subs := range r.subs {
		if _, ok := subs[key]; ok {
			r.removeLocked(resource, remote, token)
		}
	}
}

func (r *ObserverRegistry) removeLocked(resource, remote string, token []byte) {
	key := observerKey(remote, token)
	if subs, ok := r.subs[resource]; ok {
		if _, ok := subs[key]; ok {
			delete(subs, key)
			if len(subs) == 0 {
				delete(r.subs, resource)
			}
			if r.remoteCount[remote] > 0 {
				r.remoteCount[remote]--
				if r.remoteCount[remote] == 0 {
					delete(r.remoteCount, remote)
				}
			}
			r.total--
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
				r.total--
			}
		}
		if len(subs) == 0 {
			delete(r.subs, resource)
		}
	}
	delete(r.remoteCount, remote)
}

// HasObservers reports whether the remote currently holds at least one
// observe relation. The idle-session sweep uses this to avoid dropping the
// subscriptions of clients that are silent between notifications.
func (r *ObserverRegistry) HasObservers(remote string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.remoteCount[remote] > 0
}

// Count returns the total number of observe relations.
func (r *ObserverRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.total
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
// The Observe option uses the same variable-length encoding as the server's
// notification path (encodeObserveSeq), so both produce RFC 7641-compliant
// values.
func SendObserveNotification(sess core.Session, msgID uint16, token []byte, seq uint32, payload []byte) error {
	notify := Message{
		Type:      TypeCON,
		Code:      CodeContent,
		MessageID: msgID,
		Token:     token,
		Options:   map[uint16][]byte{ObserveOption: encodeObserveSeq(seq)},
		Payload:   payload,
	}
	data, err := notify.Marshal()
	if err != nil {
		return err
	}
	return sess.Send(data)
}
