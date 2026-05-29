package plugin

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/infra/pubsub"
)

type Cluster struct {
	core.BasePlugin
	nodeID  string
	topic   string
	bus     *pubsub.PubSub
	manager core.SessionManager
	cancel  func()
	stop    chan struct{}
	once    sync.Once
}

type clusterEnvelope struct {
	NodeID   string `json:"node_id"`
	Topic    string `json:"topic"`
	Protocol string `json:"protocol"`
	Payload  []byte `json:"payload"`
}

func NewCluster(nodeID string, bus *pubsub.PubSub, manager core.SessionManager) *Cluster {
	if nodeID == "" {
		nodeID = "local"
	}
	return &Cluster{nodeID: nodeID, topic: "shark.cluster.messages", bus: bus, manager: manager, stop: make(chan struct{})}
}

func (p *Cluster) Name() string  { return "cluster" }
func (p *Cluster) Priority() int { return 95 }

func (p *Cluster) WithTopic(topic string) *Cluster {
	if topic != "" {
		p.topic = topic
	}
	return p
}

func (p *Cluster) Start(buffer int) {
	if p.bus == nil || p.manager == nil {
		return
	}
	if buffer <= 0 {
		buffer = 16
	}
	p.once.Do(func() {
		ch, cancel := p.bus.Subscribe(p.topic, buffer)
		p.cancel = cancel
		go p.consume(ch)
	})
}

func (p *Cluster) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
}

func (p *Cluster) OnMessage(sess core.Session, data []byte) ([]byte, error) {
	if p.bus == nil {
		return data, nil
	}
	env := clusterEnvelope{
		NodeID:   p.nodeID,
		Topic:    p.topic,
		Protocol: string(sess.Protocol()),
		Payload:  append([]byte(nil), data...),
	}
	encoded, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	p.bus.Publish(p.topic, encoded)
	return data, nil
}

func (p *Cluster) consume(ch <-chan pubsub.Message) {
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			p.handleClusterMessage(msg.Data)
		case <-p.stop:
			return
		}
	}
}

func (p *Cluster) handleClusterMessage(data []byte) {
	var env clusterEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return
	}
	if env.NodeID == p.nodeID || env.Topic != p.topic || len(env.Payload) == 0 {
		return
	}
	_ = p.manager.Broadcast(env.Payload)
}

func (p *Cluster) Close(context.Context) error {
	p.Stop()
	return nil
}
