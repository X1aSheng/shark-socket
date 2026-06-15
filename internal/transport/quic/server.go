package quic

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
	quicgo "github.com/quic-go/quic-go"
)

type Server struct {
	opts     Options
	rt       core.Runtime
	listener *quicgo.Listener
	acceptor *shared.Acceptor
	closed   atomic.Bool
	started  atomic.Bool
	wg       sync.WaitGroup
	sessions sync.Map
}

func NewServer(opts ...Option) *Server {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Server{opts: cfg}
}

func (s *Server) Protocol() core.Protocol { return core.ProtocolQUIC }

func (s *Server) UseRuntime(rt core.Runtime) {
	s.rt = rt
}

func (s *Server) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return fmt.Errorf("quic server already started")
	}
	if s.opts.TLSConfig == nil {
		s.started.Store(false)
		return fmt.Errorf("quic tls config required")
	}
	if s.rt == nil {
		s.rt = runtime.NewRuntime(nil, nil)
	}
	s.closed.Store(false)
	s.acceptor = shared.NewAcceptor(s.opts.MaxConnections, s.opts.AcceptRate)
	ln, err := quicgo.ListenAddr(s.opts.Addr, s.opts.TLSConfig, nil)
	if err != nil {
		s.started.Store(false)
		return fmt.Errorf("quic listen %s: %w", s.opts.Addr, err)
	}
	s.listener = ln
	s.wg.Add(1)
	go s.acceptLoop(ctx)
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	_ = s.StopAccept(ctx)
	_ = s.Drain(ctx)
	return s.CloseSessions(ctx)
}

func (s *Server) StopAccept(context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.started.Store(false)
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) Drain(ctx context.Context) error {
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

func (s *Server) CloseSessions(ctx context.Context) error {
	s.sessions.Range(func(key, value any) bool {
		s.closeSession(ctx, key.(uint64), value.(*session))
		return true
	})
	return nil
}

func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (s *Server) acceptLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept(ctx)
		if err != nil {
			if s.closed.Load() || ctx.Err() != nil {
				return
			}
			continue
		}
		if s.acceptor != nil && !s.acceptor.TryAccept() {
			conn.CloseWithError(0, "server busy")
			continue
		}
		s.wg.Add(1)
		go func(conn *quicgo.Conn) {
			defer s.wg.Done()
			defer s.acceptor.Done()
			s.handleConn(conn)
		}(conn)
	}
}

func (s *Server) handleConn(conn *quicgo.Conn) {
	id := s.rt.Sessions().NextID()
	sess := newSession(id, conn, s.opts.WriteQueueSize, s.opts.WriteTimeout)
	s.sessions.Store(id, sess)
	if err := s.rt.Sessions().Register(sess); err != nil {
		s.closeSession(context.Background(), id, sess)
		return
	}
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		s.closeSession(context.Background(), id, sess)
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		sess.writeLoop()
	}()
	defer s.closeSession(context.Background(), id, sess)
	for {
		stream, err := conn.AcceptStream(sess.Context())
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func(stream *quicgo.Stream) {
			defer s.wg.Done()
			s.handleStream(sess, stream)
		}(stream)
	}
}

func (s *Server) handleStream(sess *session, stream *quicgo.Stream) {
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, int64(s.opts.MaxMessageSize)+1))
	if err != nil || len(data) > s.opts.MaxMessageSize {
		return
	}
	sess.touch()
	data, err = s.rt.Plugins().OnMessage(sess, data)
	if err != nil {
		return
	}
	if s.opts.Handler != nil {
		msg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolQUIC, Payload: data}
		_ = s.opts.Handler(sess, msg)
	}
}

func (s *Server) closeSession(ctx context.Context, id uint64, sess *session) {
	if _, loaded := s.sessions.LoadAndDelete(id); !loaded {
		return // already closed
	}
	s.rt.Sessions().Unregister(id)
	_ = sess.Close(ctx)
	s.rt.Plugins().OnClose(sess)
}

func ClientTLSConfig(insecure bool) *tls.Config {
	return &tls.Config{InsecureSkipVerify: insecure, NextProtos: []string{"shark-socket-quic"}}
}

var (
	_ core.Server              = (*Server)(nil)
	_ core.RuntimeConfigurable = (*Server)(nil)
	_ core.StagedServer        = (*Server)(nil)
)
