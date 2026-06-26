package plugin

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/pubsub"
)

// Cluster distributes messages across nodes via PubSub.
// NOTE: Cross-node broadcast amplification is possible if application-layer
// handlers echo messages back through OnMessage. The env.NodeID check only
// prevents same-node feedback, not cross-node loops. Ensure handlers do not
// re-publish received cluster messages to avoid amplification.
type Cluster struct {
	core.BasePlugin
	nodeID   string
	topic    string
	bus      *pubsub.PubSub
	manager  core.SessionManager
	cancel   func()
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	logger   core.Logger
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
	return &Cluster{nodeID: nodeID, topic: "shark.cluster.messages", bus: bus, manager: manager, stop: make(chan struct{}), logger: core.NopLogger()}
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
	// Recreate stop channel and stopOnce if previously stopped (supports restart).
	select {
	case <-p.stop:
		p.stop = make(chan struct{})
		p.stopOnce = sync.Once{}
	default:
	}
	ch, cancel := p.bus.Subscribe(p.topic, buffer)
	p.cancel = cancel
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.consume(ch)
	}()
}

func (p *Cluster) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.stopOnce.Do(func() { close(p.stop) })
	p.wg.Wait()
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
	if err := p.manager.Broadcast(env.Payload); err != nil {
		p.logger.Warn("cluster: broadcast error", "error", err)
	}
}

func (p *Cluster) Close(context.Context) error {
	p.Stop()
	return nil
}
