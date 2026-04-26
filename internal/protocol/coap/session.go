package coap

import (
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// CoAPSession implements a CoAP session with CON retransmission and message dedup.
type CoAPSession struct {
	*session.BaseSession
	conn       *net.UDPConn
	addr       *net.UDPAddr
	mu         sync.Mutex
	pendingACKs map[uint16]*pendingMsg
	msgCache   map[uint16]time.Time
	msgCacheMu sync.RWMutex
	closeOnce  sync.Once
}

type pendingMsg struct {
	msg      []byte
	attempts int
	sendAt   time.Time
}

// Compile-time verification.
var _ types.RawSession = (*CoAPSession)(nil)

// NewCoAPSession creates a new CoAP session.
func NewCoAPSession(id uint64, conn *net.UDPConn, addr *net.UDPAddr) *CoAPSession {
	s := &CoAPSession{
		BaseSession: session.NewBase(id, types.CoAP, addr, conn.LocalAddr()),
		conn:        conn,
		addr:        addr,
		pendingACKs: make(map[uint16]*pendingMsg),
		msgCache:    make(map[uint16]time.Time),
	}
	s.SetState(types.Active)
	return s
}

// Send writes data to the UDP address.
func (s *CoAPSession) Send(data []byte) error {
	if !s.IsAlive() {
		return errs.ErrSessionClosed
	}
	s.mu.Lock()
	_, err := s.conn.WriteToUDP(data, s.addr)
	s.mu.Unlock()
	return err
}

// SendTyped encodes and sends.
func (s *CoAPSession) SendTyped(msg []byte) error {
	return s.Send(msg)
}

// Close marks the session as closed.
func (s *CoAPSession) Close() error {
	s.closeOnce.Do(func() {
		s.SetState(types.Closed)
		s.DoClose()
	})
	return nil
}

// TrackCON registers a CON message for potential retransmission.
func (s *CoAPSession) TrackCON(msgID uint16, data []byte) {
	s.mu.Lock()
	s.pendingACKs[msgID] = &pendingMsg{
		msg:      data,
		attempts: 1,
		sendAt:   time.Now(),
	}
	s.mu.Unlock()
}

// Acknowledge removes a CON message from the pending list.
func (s *CoAPSession) Acknowledge(msgID uint16) {
	s.mu.Lock()
	delete(s.pendingACKs, msgID)
	s.mu.Unlock()
}

// ResetCON removes a CON message (RST received).
func (s *CoAPSession) ResetCON(msgID uint16) {
	s.Acknowledge(msgID)
}

// IsDuplicate checks if a MessageID was recently seen.
func (s *CoAPSession) IsDuplicate(msgID uint16) bool {
	s.msgCacheMu.RLock()
	_, found := s.msgCache[msgID]
	s.msgCacheMu.RUnlock()
	return found
}

// RecordMessageID adds a MessageID to the dedup cache.
// Evicts oldest entries when cache exceeds maxSize.
func (s *CoAPSession) RecordMessageID(msgID uint16) {
	s.msgCacheMu.Lock()
	s.msgCache[msgID] = time.Now()
	// Evict oldest entries when over capacity
	const maxCacheSize = 500
	if len(s.msgCache) > maxCacheSize {
		var oldestID uint16
		var oldestTime time.Time
		first := true
		for id, t := range s.msgCache {
			if first || t.Before(oldestTime) {
				oldestID = id
				oldestTime = t
				first = false
			}
		}
		delete(s.msgCache, oldestID)
	}
	s.msgCacheMu.Unlock()
}

// PendingCount returns the number of pending CON messages.
func (s *CoAPSession) PendingCount() int {
	s.mu.Lock()
	n := len(s.pendingACKs)
	s.mu.Unlock()
	return n
}

// Addr returns the remote UDP address.
func (s *CoAPSession) Addr() *net.UDPAddr {
	return s.addr
}
