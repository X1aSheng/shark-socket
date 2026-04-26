package websocket

import (
	"sync"

	"github.com/gorilla/websocket"
	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// WSSession implements types.Session for WebSocket connections.
type WSSession struct {
	*session.BaseSession
	conn     *websocket.Conn
	writeMu  sync.Mutex
	closeOnce sync.Once
}

// Compile-time verification.
var _ types.RawSession = (*WSSession)(nil)

// NewWSSession creates a new WebSocket session.
func NewWSSession(id uint64, conn *websocket.Conn) *WSSession {
	s := &WSSession{
		BaseSession: session.NewBase(id, types.WebSocket, conn.RemoteAddr(), conn.LocalAddr()),
		conn:        conn,
	}
	s.SetState(types.Active)
	return s
}

// Send writes binary data to the WebSocket.
func (s *WSSession) Send(data []byte) error {
	if !s.IsAlive() {
		return errs.ErrSessionClosed
	}
	s.writeMu.Lock()
	err := s.conn.WriteMessage(websocket.BinaryMessage, data)
	s.writeMu.Unlock()
	return err
}

// SendTyped encodes and sends.
func (s *WSSession) SendTyped(msg []byte) error {
	return s.Send(msg)
}

// SendText writes text data to the WebSocket.
func (s *WSSession) SendText(data []byte) error {
	if !s.IsAlive() {
		return errs.ErrSessionClosed
	}
	s.writeMu.Lock()
	err := s.conn.WriteMessage(websocket.TextMessage, data)
	s.writeMu.Unlock()
	return err
}

// sendPing sends a ping frame.
func (s *WSSession) sendPing() error {
	s.writeMu.Lock()
	err := s.conn.WriteMessage(websocket.PingMessage, nil)
	s.writeMu.Unlock()
	return err
}

// Close gracefully closes the WebSocket session.
func (s *WSSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.SetState(types.Closing)
		s.writeMu.Lock()
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		_ = s.conn.WriteMessage(websocket.CloseMessage, closeMsg)
		s.writeMu.Unlock()
		s.SetState(types.Closed)
		s.DoClose()
		err = s.conn.Close()
	})
	return err
}

// Conn returns the underlying gorilla/websocket connection.
func (s *WSSession) Conn() *websocket.Conn {
	return s.conn
}
