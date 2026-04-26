package coap

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// Server is a CoAP protocol server.
type Server struct {
	opts     Options
	conn     *net.UDPConn
	sessions sync.Map // addr.String() -> *CoAPSession
	handler  types.RawHandler
	chain    *plugin.Chain
	wg       sync.WaitGroup
	closed   atomic.Bool
	idGen    atomic.Uint64
	cancel   context.CancelFunc
	ctx      context.Context
}

// Compile-time verification.
var _ types.Server = (*Server)(nil)

// NewServer creates a new CoAP server.
func NewServer(handler types.RawHandler, opts ...Option) *Server {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	return &Server{
		opts:    o,
		handler: handler,
	}
}

// Start begins listening for CoAP messages.
func (s *Server) Start() error {
	if err := s.opts.validate(); err != nil {
		return err
	}

	addr, err := net.ResolveUDPAddr("udp", s.opts.Addr())
	if err != nil {
		return fmt.Errorf("coap: resolve addr %s: %w", s.opts.Addr(), err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("coap: listen on %s: %w", s.opts.Addr(), err)
	}
	s.conn = conn
	s.ctx, s.cancel = context.WithCancel(context.Background())

	if len(s.opts.Plugins) > 0 {
		s.chain = plugin.NewChain(s.opts.Plugins...)
	}

	s.wg.Add(2)
	go s.readLoop()
	go s.retransmitLoop()

	log.Printf("CoAP server listening on %s", s.opts.Addr())
	return nil
}

func (s *Server) readLoop() {
	defer s.wg.Done()

	buf := make([]byte, 65535)
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if s.closed.Load() {
				return
			}
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		// Parse CoAP message
		msg, err := ParseMessage(data)
		if err != nil {
			continue // silently drop malformed messages
		}

		sess := s.getOrCreateSession(addr)
		if sess == nil {
			continue
		}
		sess.TouchActive()

		// Message deduplication
		if sess.IsDuplicate(msg.MessageID) {
			// Already processed, resend ACK if needed
			if msg.Type == CON {
				ack := NewACK(msg, CodeContent, nil)
				ackData, _ := ack.Serialize()
				_ = sess.Send(ackData)
			}
			continue
		}
		sess.RecordMessageID(msg.MessageID)

		// Handle CON with ACK
		if msg.Type == CON {
			ack := NewACK(msg, CodeChanged, nil)
			ackData, _ := ack.Serialize()
			_ = sess.Send(ackData)
		}

		// Handle RST
		if msg.Type == RST {
			sess.ResetCON(msg.MessageID)
			continue
		}

		// Plugin chain
		payload := msg.Payload
		if s.chain != nil && len(payload) > 0 {
			var chainErr error
			payload, chainErr = s.chain.OnMessage(sess, payload)
			if chainErr != nil {
				continue
			}
		}

		if s.handler != nil && len(payload) > 0 {
			handlerMsg := types.NewRawMessage(sess.ID(), types.CoAP, payload)
			_ = s.handler(sess, handlerMsg)
		}
	}
}

func (s *Server) retransmitLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.opts.AckTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.sessions.Range(func(key, val any) bool {
				sess := val.(*CoAPSession)
				sess.mu.Lock()
				for msgID, pm := range sess.pendingACKs {
					if time.Since(pm.sendAt) > s.opts.AckTimeout*time.Duration(1<<pm.attempts) {
						if pm.attempts >= s.opts.MaxRetransmit {
							delete(sess.pendingACKs, msgID)
							continue
						}
						pm.attempts++
						pm.sendAt = time.Now()
						_ = sess.Send(pm.msg)
					}
				}
				sess.mu.Unlock()
				return true
			})
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Server) getOrCreateSession(addr *net.UDPAddr) *CoAPSession {
	key := addr.String()
	if val, ok := s.sessions.Load(key); ok {
		return val.(*CoAPSession)
	}

	id := s.idGen.Add(1)
	sess := NewCoAPSession(id, s.conn, addr)

	if s.chain != nil {
		if err := s.chain.OnAccept(sess); err != nil {
			sess.Close()
			return nil
		}
	}

	actual, _ := s.sessions.LoadOrStore(key, sess)
	return actual.(*CoAPSession)
}

// Stop shuts down the CoAP server.
func (s *Server) Stop(ctx context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		s.conn.Close()
	}
	s.sessions.Range(func(key, val any) bool {
		val.(*CoAPSession).Close()
		s.sessions.Delete(key)
		return true
	})
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Protocol returns CoAP.
func (s *Server) Protocol() types.ProtocolType { return types.CoAP }

// Addr returns the listen address. Only valid after Start().
func (s *Server) Addr() net.Addr {
	if s.conn == nil {
		return nil
	}
	return s.conn.LocalAddr()
}
