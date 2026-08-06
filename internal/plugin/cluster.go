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
	nodeID  string
	topic   string
	bus     *pubsub.PubSub
	manager core.SessionManager
	lc      lifecycle
	mu      sync.RWMutex // guards logger
	logger  core.Logger
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
	return &Cluster{nodeID: nodeID, topic: "shark.cluster.messages", bus: bus, manager: manager, logger: core.NopLogger()}
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
	stop, ok := p.lc.begin()
	if !ok {
		return
	}
	if p.bus == nil || p.manager == nil {
		p.lc.done()
		return
	}
	if buffer <= 0 {
		buffer = 16
	}
	ch, cancel := p.bus.Subscribe(p.topic, buffer)
	go func() {
		defer p.lc.done()
		defer cancel() // unsubscribe the consumer when it exits
		p.consume(ch, stop)
	}()
}

func (p *Cluster) Stop() {
	p.lc.shutdown()
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

// SetLogger sets the logger used for operational messages.
func (p *Cluster) SetLogger(logger core.Logger) {
	if logger == nil {
		return
	}
	p.mu.Lock()
	p.logger = logger
	p.mu.Unlock()
}

func (p *Cluster) consume(ch <-chan pubsub.Message, stop <-chan struct{}) {
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			p.handleClusterMessage(msg.Data)
		case <-stop:
			return
		}
	}
}

func (p *Cluster) handleClusterMessage(data []byte) {
	var env clusterEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		p.mu.RLock()
		logger := p.logger
		p.mu.RUnlock()
		if logger != nil {
			logger.Warn("cluster: dropping malformed message", "error", err)
		}
		return
	}
	if env.NodeID == p.nodeID || env.Topic != p.topic || len(env.Payload) == 0 {
		return
	}
	if err := p.manager.Broadcast(env.Payload); err != nil {
		p.mu.RLock()
		logger := p.logger
		p.mu.RUnlock()
		if logger != nil {
			logger.Warn("cluster: broadcast error", "error", err)
		}
	}
}

func (p *Cluster) Close(context.Context) error {
	p.Stop()
	return nil
}
