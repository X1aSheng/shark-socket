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
	conn        *net.UDPConn
	addr        *net.UDPAddr
	mu          sync.Mutex
	pendingACKs map[uint16]*pendingMsg
	msgCache    map[uint16]time.Time
	msgCacheMu  sync.RWMutex
	cacheSize   int
	closeOnce   sync.Once
}

type pendingMsg struct {
	msg      []byte
	attempts int
	sendAt   time.Time
}

// Compile-time verification.
var _ types.RawSession = (*CoAPSession)(nil)

// NewCoAPSession creates a new CoAP session.
func NewCoAPSession(id uint64, conn *net.UDPConn, addr *net.UDPAddr, cacheSize int) *CoAPSession {
	s := &CoAPSession{
		BaseSession: session.NewBase(id, types.CoAP, addr, conn.LocalAddr()),
		conn:        conn,
		addr:        addr,
		pendingACKs: make(map[uint16]*pendingMsg),
		msgCache:    make(map[uint16]time.Time),
		cacheSize:   cacheSize,
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

// CheckAndRecord atomically checks for duplicate MessageID and records it.
// Returns true if the message was already seen (duplicate).
func (s *CoAPSession) CheckAndRecord(msgID uint16) bool {
	s.msgCacheMu.Lock()
	defer s.msgCacheMu.Unlock()

	if _, found := s.msgCache[msgID]; found {
		return true
	}
	s.msgCache[msgID] = time.Now()

	// Evict oldest entry when over capacity
	if len(s.msgCache) > s.cacheSize {
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
	return false
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
