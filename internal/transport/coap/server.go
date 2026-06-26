package coap

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
	"github.com/pion/dtls/v3"
)

// seenKey is a struct key for the CoAP dedup map, avoiding per-message
// string allocation from fmt.Sprintf.
type seenKey struct {
	remote string
	msgID  uint16
}

type Server struct {
	opts      Options
	rt        core.Runtime
	udpConn   *net.UDPConn
	dtlsLn    net.Listener
	dtlsConns sync.Map // active DTLS connections, closed on shutdown
	lastMsgID atomic.Uint32
	closed    atomic.Bool
	started   atomic.Bool
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	sessions  sync.Map
	seen      sync.Map
	observers *ObserverRegistry
}

func NewServer(opts ...Option) *Server {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Server{opts: cfg, observers: NewObserverRegistry()}
}

func (s *Server) Protocol() core.Protocol { return core.ProtocolCoAP }

func (s *Server) UseRuntime(rt core.Runtime) {
	s.rt = rt
}

func (s *Server) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return fmt.Errorf("coap server already started")
	}
	if s.rt == nil {
		s.rt = runtime.NewRuntime(nil, nil)
	}
	s.closed.Store(false)
	addr, err := net.ResolveUDPAddr("udp", s.opts.Addr)
	if err != nil {
		s.started.Store(false)
		return fmt.Errorf("coap resolve %s: %w", s.opts.Addr, err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	if s.opts.TLSConfig != nil {
		ln, err := dtls.Listen("udp", addr, shared.DTLSConfig(s.opts.TLSConfig))
		if err != nil {
			cancel()
			s.started.Store(false)
			return fmt.Errorf("coap dtls listen %s: %w", s.opts.Addr, err)
		}
		s.dtlsLn = ln
		s.wg.Add(2)
		go s.dtlsAcceptLoop(runCtx)
		go s.seenCleanupLoop(runCtx)
	} else {
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			cancel()
			s.started.Store(false)
			return fmt.Errorf("coap listen %s: %w", s.opts.Addr, err)
		}
		s.udpConn = conn
		s.wg.Add(3)
		go s.readLoop(runCtx)
		go s.sweepLoop(runCtx)
		go s.seenCleanupLoop(runCtx)
	}
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
	if s.cancel != nil {
		s.cancel()
	}
	// Close individual DTLS connections to unblock read goroutines
	s.dtlsConns.Range(func(key, value any) bool {
		if conn, ok := value.(net.Conn); ok {
			_ = conn.Close()
		}
		return true
	})
	if s.dtlsLn != nil {
		return s.dtlsLn.Close()
	}
	if s.udpConn != nil {
		return s.udpConn.Close()
	}
	return nil
}

func (s *Server) Drain(ctx context.Context) error {
	// Wait for read/sweep/cleanup goroutines to finish. The drain goroutine
	// is fire-and-forget: StopAccept already closed the listener/connection
	// and cancelled the context, so all tracked goroutines exit promptly.
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
		s.closeSession(ctx, key, value.(*session))
		return true
	})
	return nil
}

func (s *Server) Addr() net.Addr {
	if s.dtlsLn != nil {
		return s.dtlsLn.Addr()
	}
	if s.udpConn == nil {
		return nil
	}
	return s.udpConn.LocalAddr()
}

func (s *Server) SessionCount() int {
	count := 0
	s.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// dtlsAcceptLoop accepts DTLS connections. Each connection is handled
// in its own goroutine, similar to TCP accept.
func (s *Server) dtlsAcceptLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		conn, err := s.dtlsLn.Accept()
		if err != nil {
			if s.closed.Load() || ctx.Err() != nil {
				return
			}
			continue
		}
		s.wg.Add(1)
		go s.handleDTLSConn(ctx, conn)
	}
}

// handleDTLSConn reads CoAP messages from a single DTLS connection.
func (s *Server) handleDTLSConn(ctx context.Context, conn net.Conn) {
	defer s.wg.Done()

	id := s.rt.Sessions().NextID()
	sess := newDTLSSession(id, conn)
	s.sessions.Store(id, sess)
	s.dtlsConns.Store(id, conn)
	defer func() {
		s.dtlsConns.Delete(id)
		s.sessions.Delete(id)
		s.rt.Sessions().Unregister(id)
		s.rt.Plugins().OnClose(sess)
	}()

	if err := s.rt.Sessions().Register(sess); err != nil {
		_ = sess.Close(context.Background())
		return
	}
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		_ = sess.Close(context.Background())
		return
	}

	buf := make([]byte, s.opts.MaxDatagram)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		sess.touch()
		s.handleCoAPMessage(sess, buf[:n])
	}
}

// readLoop is the plain UDP read loop.
func (s *Server) readLoop(ctx context.Context) {
	defer s.wg.Done()
	buf := make([]byte, s.opts.MaxDatagram)
	for {
		n, addr, err := s.udpConn.ReadFromUDP(buf)
		if err != nil {
			if s.closed.Load() || ctx.Err() != nil {
				return
			}
			continue
		}
		sess := s.getOrCreateUDPSession(addr)
		if sess == nil {
			continue
		}
		sess.touch()
		s.handleCoAPMessage(sess, buf[:n])
	}
}

// handleCoAPMessage parses and processes a CoAP message for a session.
func (s *Server) handleCoAPMessage(sess *session, data []byte) {
	msg, err := Parse(data)
	if err != nil {
		return
	}
	sk := seenKey{remote: sess.remote.String(), msgID: msg.MessageID}
	if _, loaded := s.seen.LoadOrStore(sk, struct{}{}); loaded && msg.Type == TypeCON {
		if err := s.sendACK(sess, msg, CodeValid, nil); err != nil && s.rt != nil {
			s.rt.Logger().Warn("coap dedup ack send failed", "error", err)
		}
		return
	}
	if msg.Type == TypeRST || msg.Type == TypeACK {
		return
	}

	s.handleObserve(sess, msg)

	payload, err := s.rt.Plugins().OnMessage(sess, msg.Payload)
	if err != nil {
		if err != core.ErrPluginDrop {
			_ = sess.Close(context.Background())
		}
		if msg.Type == TypeCON {
			if err := s.sendACK(sess, msg, CodeInternalServerError, nil); err != nil && s.rt != nil {
				s.rt.Logger().Warn("coap error ack send failed", "error", err)
			}
		}
		return
	}
	var responsePayload []byte
	if s.opts.Responder != nil && len(payload) > 0 {
		handlerMsg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolCoAP, Payload: payload}
		responsePayload, err = s.opts.Responder(sess, handlerMsg)
		if err != nil {
			_ = sess.Close(context.Background())
			return
		}
	} else if s.opts.Handler != nil && len(payload) > 0 {
		handlerMsg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolCoAP, Payload: payload}
		if err := s.opts.Handler(sess, handlerMsg); err != nil {
			_ = sess.Close(context.Background())
		}
	}
	if msg.Type == TypeCON {
		resp := ACK(msg, responseCode(msg.Code), responsePayload)
		s.addObserveSeq(sess, msg, &resp)
		if err := s.sendACKMsg(sess, resp); err != nil && s.rt != nil {
			s.rt.Logger().Warn("coap ack send failed", "error", err)
		}
	}
}

func (s *Server) handleObserve(sess *session, msg Message) {
	if msg.Code != CodeGet {
		return
	}
	obsVal, hasObserve := msg.Options[ObserveOption]
	resource := string(msg.Payload)
	remote := sess.remote.String()
	if hasObserve && len(obsVal) > 0 {
		reg := obsVal[0]
		if reg == 0 {
			s.observers.Register(resource, remote, msg.Token)
		} else if reg == 1 {
			s.observers.Remove(resource, remote, msg.Token)
		}
	}
}

func (s *Server) addObserveSeq(sess *session, req Message, resp *Message) {
	if req.Code != CodeGet {
		return
	}
	resource := string(req.Payload)
	remote := sess.remote.String()
	key := observerKey(remote, req.Token)
	s.observers.mu.RLock()
	subs, ok := s.observers.subs[resource]
	var obs *Observer
	if ok {
		obs = subs[key]
	}
	s.observers.mu.RUnlock()
	if obs != nil {
		seq := obs.NextSeq()
		if resp.Options == nil {
			resp.Options = make(map[uint16][]byte)
		}
		resp.Options[ObserveOption] = encodeObserveSeq(seq)
	}
}

// NotifyObservers sends notifications to all observers of a resource.
func (s *Server) NotifyObservers(resource string, payload []byte) {
	for _, obs := range s.observers.Notify(resource) {
		sess := s.findSessionByRemote(obs.Remote)
		if sess == nil {
			s.observers.Remove(resource, obs.Remote, obs.Token)
			continue
		}
		seq := obs.NextSeq()
		seqBuf := encodeObserveSeq(seq)
		notify := Message{
			Type:      TypeCON,
			Code:      CodeContent,
			MessageID: s.nextMessageID(),
			Token:     obs.Token,
			Options:   map[uint16][]byte{ObserveOption: seqBuf},
			Payload:   payload,
		}
		if err := s.sendACKMsg(sess, notify); err != nil {
			s.observers.Remove(resource, obs.Remote, obs.Token)
		}
	}
}

func (s *Server) findSessionByRemote(remote string) *session {
	var found *session
	s.sessions.Range(func(_, value any) bool {
		sess := value.(*session)
		if sess.remote.String() == remote {
			found = sess
			return false
		}
		return true
	})
	return found
}

func (s *Server) nextMessageID() uint16 {
	return uint16(s.lastMsgID.Add(1) % 65536)
}

func (s *Server) sweepLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.opts.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			s.sessions.Range(func(key, value any) bool {
				sess := value.(*session)
				if now.Sub(sess.LastActiveAt()) > s.opts.SessionTTL {
					s.closeSession(context.Background(), key, sess)
				}
				return true
			})
		case <-ctx.Done():
			return
		}
	}
}

// seenCleanupLoop periodically clears the dedup map to prevent unbounded memory
// growth from transient clients (e.g., IoT devices behind rotating NAT).
// CoAP MessageIDs are 16-bit (wrapping at 65536), so a 5-minute interval is safe.
func (s *Server) seenCleanupLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.seen.Clear()
		case <-ctx.Done():
			return
		}
	}
}

// getOrCreateUDPSession finds or creates a session for a UDP peer address.
func (s *Server) getOrCreateUDPSession(addr *net.UDPAddr) *session {
	key := addr.String()
	if value, ok := s.sessions.Load(key); ok {
		return value.(*session)
	}
	// Create provisional session; only allocate ID if it's actually new
	sess := newUDPSession(0, s.udpConn, addr)
	actual, loaded := s.sessions.LoadOrStore(key, sess)
	if loaded {
		_ = sess.Close(context.Background())
		return actual.(*session)
	}
	sess.id = s.rt.Sessions().NextID()
	if err := s.rt.Sessions().Register(sess); err != nil {
		s.sessions.Delete(key)
		_ = sess.Close(context.Background())
		return nil
	}
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		s.closeSession(context.Background(), key, sess)
		return nil
	}
	return sess
}

func (s *Server) sendACK(sess *session, req Message, code byte, payload []byte) error {
	return s.sendACKMsg(sess, ACK(req, code, payload))
}

func (s *Server) sendACKMsg(sess *session, msg Message) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	return sess.Send(data)
}

// encodeObserveSeq encodes a sequence number in minimal big-endian bytes.
// RFC 7641 uses variable-length encoding; 3 bytes covers up to 16M notifications.
func encodeObserveSeq(seq uint32) []byte {
	switch {
	case seq == 0:
		return []byte{0}
	case seq <= 0xFF:
		return []byte{byte(seq)}
	case seq <= 0xFFFF:
		return []byte{byte(seq >> 8), byte(seq)}
	default:
		return []byte{byte(seq >> 16), byte(seq >> 8), byte(seq)}
	}
}

func (s *Server) closeSession(ctx context.Context, key any, sess *session) {
	if _, loaded := s.sessions.LoadAndDelete(key); !loaded {
		return
	}
	s.observers.RemoveBySession(sess.remote.String())
	s.rt.Sessions().Unregister(sess.ID())
	_ = sess.Close(ctx)
	s.rt.Plugins().OnClose(sess)
}

func responseCode(code byte) byte {
	switch code {
	case CodePost:
		return CodeCreated
	case CodePut:
		return CodeChanged
	case CodeDelete:
		return CodeDeleted
	default:
		return CodeContent
	}
}


var (
	_ core.Server              = (*Server)(nil)
	_ core.RuntimeConfigurable = (*Server)(nil)
	_ core.StagedServer        = (*Server)(nil)
)
