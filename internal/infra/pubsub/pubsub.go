package pubsub

import "sync"

type Message struct {
	Topic string
	Data  []byte
}

type PubSub struct {
	mu      sync.RWMutex
	subs    map[string][]chan Message
	dropped map[string]uint64
}

func New() *PubSub {
	return &PubSub{subs: make(map[string][]chan Message), dropped: make(map[string]uint64)}
}

func (p *PubSub) Subscribe(topic string, buffer int) (<-chan Message, func()) {
	ch := make(chan Message, buffer)
	p.mu.Lock()
	p.subs[topic] = append(p.subs[topic], ch)
	p.mu.Unlock()
	cancel := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		subs := p.subs[topic]
		for i, sub := range subs {
			if sub == ch {
				// Drop the topic key when the last subscriber leaves; otherwise
				// transient topics accumulate empty-slice entries forever.
				if len(subs) == 1 {
					delete(p.subs, topic)
					// The dropped counter is only observable through
					// subscribers; without any, keep the map from leaking
					// per-topic counters forever.
					delete(p.dropped, topic)
				} else {
					p.subs[topic] = append(subs[:i], subs[i+1:]...)
				}
				close(ch)
				return
			}
		}
	}
	return ch, cancel
}

func (p *PubSub) Publish(topic string, data []byte) int {
	p.mu.RLock()
	// Hold the read lock during iteration + send to prevent cancel()
	// from closing a channel while we're sending to it.
	msg := Message{Topic: topic, Data: append([]byte(nil), data...)}
	delivered := 0
	dropped := 0
	for _, sub := range p.subs[topic] {
		select {
		case sub <- msg:
			delivered++
		default:
			dropped++
		}
	}
	p.mu.RUnlock()
	// The dropped counter is written under the exclusive lock: concurrent
	// Publish calls both hold RLock during iteration and would otherwise race
	// on the map (concurrent map writes crash the process).
	if dropped > 0 {
		p.mu.Lock()
		p.dropped[topic] += uint64(dropped)
		p.mu.Unlock()
	}
	return delivered
}

// Dropped returns the number of messages dropped for a topic because a
// subscriber's buffer was full (non-blocking Publish). This surfaces the
// otherwise-silent drops so operators can raise the buffer or back off.
func (p *PubSub) Dropped(topic string) uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dropped[topic]
}
