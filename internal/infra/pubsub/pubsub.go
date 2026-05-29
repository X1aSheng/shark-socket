package pubsub

import "sync"

type Message struct {
	Topic string
	Data  []byte
}

type PubSub struct {
	mu   sync.RWMutex
	subs map[string][]chan Message
}

func New() *PubSub {
	return &PubSub{subs: make(map[string][]chan Message)}
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
				p.subs[topic] = append(subs[:i], subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, cancel
}

func (p *PubSub) Publish(topic string, data []byte) int {
	p.mu.RLock()
	subs := append([]chan Message(nil), p.subs[topic]...)
	p.mu.RUnlock()
	msg := Message{Topic: topic, Data: append([]byte(nil), data...)}
	delivered := 0
	for _, sub := range subs {
		select {
		case sub <- msg:
			delivered++
		default:
		}
	}
	return delivered
}
