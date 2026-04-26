package udp

import (
	"context"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yourname/shark-socket/internal/plugin"
	"github.com/yourname/shark-socket/internal/types"
)

// Server is a UDP protocol server.
type Server struct {
	opts     Options
	conn     *net.UDPConn
	sessions sync.Map // addr.String() -> *UDPSession
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

// NewServer creates a new UDP server.
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

// Start begins listening for UDP packets.
func (s *Server) Start() error {
	addr, err := net.ResolveUDPAddr("udp", s.opts.Addr())
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	s.conn = conn
	s.ctx, s.cancel = context.WithCancel(context.Background())

	if len(s.opts.Plugins) > 0 {
		s.chain = plugin.NewChain(s.opts.Plugins...)
	}

	s.wg.Add(2)
	go s.readLoop()
	go s.sweepLoop()

	log.Printf("UDP server listening on %s", s.opts.Addr())
	return nil
}

func (s *Server) readLoop() {
	defer s.wg.Done()

	buf := make([]byte, 65535)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if s.closed.Load() {
				return
			}
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		sess := s.getOrCreateSession(addr)
		if sess == nil {
			continue
		}
		sess.TouchActive()

		// Plugin chain
		if s.chain != nil {
			var chainErr error
			data, chainErr = s.chain.OnMessage(sess, data)
			if chainErr != nil {
				continue
			}
		}

		if s.handler != nil {
			msg := types.NewRawMessage(sess.ID(), types.UDP, data)
			_ = s.handler(sess, msg)
		}
	}
}

func (s *Server) sweepLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			s.sessions.Range(func(key, val any) bool {
				sess := val.(*UDPSession)
				if now.Sub(sess.LastActiveAt()) > s.opts.SessionTTL {
					sess.Close()
					s.sessions.Delete(key)
				}
				return true
			})
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Server) getOrCreateSession(addr *net.UDPAddr) *UDPSession {
	key := addr.String()
	if val, ok := s.sessions.Load(key); ok {
		return val.(*UDPSession)
	}

	id := s.idGen.Add(1)
	sess := NewUDPSession(id, s.conn, addr)

	if s.chain != nil {
		if err := s.chain.OnAccept(sess); err != nil {
			sess.Close()
			return nil
		}
	}

	actual, _ := s.sessions.LoadOrStore(key, sess)
	return actual.(*UDPSession)
}

// Stop shuts down the UDP server.
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
		val.(*UDPSession).Close()
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

// Protocol returns UDP.
func (s *Server) Protocol() types.ProtocolType { return types.UDP }

// SessionCount returns the number of active pseudo-sessions.
func (s *Server) SessionCount() int {
	count := 0
	s.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
