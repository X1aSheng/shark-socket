package quic

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
	"github.com/quic-go/quic-go"
)

// Server is a QUIC protocol server.
type Server struct {
	opts    Options
	listener *quic.Listener
	handler types.RawHandler
	chain   *plugin.Chain
	manager *session.Manager
	wg      sync.WaitGroup
	closed  atomic.Bool
}

// Compile-time verification.
var _ types.Server = (*Server)(nil)

// NewServer creates a new QUIC server.
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

// Start begins serving QUIC.
func (s *Server) Start() error {
	if err := s.opts.validate(); err != nil {
		return err
	}

	if s.manager == nil {
		s.manager = session.NewManager()
	}

	if len(s.opts.Plugins) > 0 {
		s.chain = plugin.NewChain(s.opts.Plugins...)
	}

	ln, err := quic.ListenAddr(s.opts.Addr(), s.opts.TLSConfig, nil)
	if err != nil {
		return fmt.Errorf("quic: listen on %s: %w", s.opts.Addr(), err)
	}
	s.listener = ln

	s.wg.Add(1)
	go s.acceptLoop()

	log.Printf("QUIC server listening on %s", s.listener.Addr())
	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept(context.Background())
		if err != nil {
			if s.closed.Load() {
				return
			}
			log.Printf("QUIC accept error: %v", err)
			continue
		}

		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn any) {
	defer s.wg.Done()

	id := s.manager.NextID()
	qconn := conn.(*quic.Conn)
	sess := newSession(id, qconn, s.opts.WriteQueueSize)

	if err := s.manager.Register(sess); err != nil {
		_ = qconn.CloseWithError(0, "session limit exceeded")
		return
	}

	if s.chain != nil {
		if err := s.chain.OnAccept(sess); err != nil {
			s.manager.Unregister(id)
			_ = qconn.CloseWithError(0, "rejected")
			return
		}
	}

	sess.startWriteLoop()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.handleStreams(qconn, sess)
	}()

	<-sess.Context().Done()

	s.manager.Unregister(id)
	if s.chain != nil {
		s.chain.OnClose(sess)
	}
}

func (s *Server) handleStreams(conn *quic.Conn, sess *Session) {
	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}

		s.wg.Add(1)
		go func(st *quic.Stream) {
			defer s.wg.Done()
			s.handleStream(st, sess)
		}(stream)
	}
}

func (s *Server) handleStream(stream *quic.Stream, sess *Session) {
	defer stream.Close()

	buf := make([]byte, s.opts.MaxMessageSize)
	for {
		n, err := stream.Read(buf)
		if err != nil {
			return
		}

		sess.TouchActive()

		if s.chain != nil {
			data, err := s.chain.OnMessage(sess, buf[:n])
			if err != nil {
				return
			}
			n = len(data)
			if n == 0 {
				continue
			}
		}

		if s.handler != nil {
			msg := types.NewRawMessage(sess.ID(), types.QUIC, buf[:n])
			_ = s.handler(sess, msg)
		}
	}
}

// Stop gracefully shuts down the QUIC server.
func (s *Server) Stop(ctx context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	if s.listener != nil {
		_ = s.listener.Close()
	}

	s.manager.Close()

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

// Protocol returns QUIC.
func (s *Server) Protocol() types.ProtocolType { return types.QUIC }

// Addr returns the listen address. Only valid after Start().
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// SetManager sets the session manager from outside (e.g., from Gateway).
func (s *Server) SetManager(m *session.Manager) {
	s.manager = m
}

// Manager returns the session manager.
func (s *Server) Manager() *session.Manager {
	return s.manager
}

// SetHandler sets the raw handler.
func (s *Server) SetHandler(h types.RawHandler) {
	s.handler = h
}