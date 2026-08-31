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

// notifyKey identifies a pending CON observe notification by remote + msgID,
// so a msgID reuse across observers (the ID wraps at 65536) cannot overwrite or
// spuriously clear another observer's entry.
type notifyKey struct {
	remote string
	msgID  uint16
}

// pendingNotify tracks an unacknowledged CON observe notification for
// retransmission (RFC 7641 / RFC 7252 reliability).
type pendingNotify struct {
	data     []byte
	attempts int
}

const (
	maxRetransmit      = 4
	retransmitInterval = 2 * time.Second
)

type Server struct {
	opts            Options
	rt              core.Runtime
	udpConn         *net.UDPConn
	dtlsLn          net.Listener
	dtlsConns       sync.Map // active DTLS connections, closed on shutdown
	lastMsgID       atomic.Uint32
	closed          atomic.Bool
	started         atomic.Bool
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	sessions        sync.Map
	seen            sync.Map
	observers       *ObserverRegistry
	retransmitMu    sync.Mutex
	pendingNotifies map[notifyKey]pendingNotify
}

func NewServer(opts ...Option) *Server {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Server{opts: cfg, observers: NewObserverRegistry(), pendingNotifies: make(map[notifyKey]pendingNotify)}
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
		s.wg.Add(3)
		go s.dtlsAcceptLoop(runCtx)
		go s.seenCleanupLoop(runCtx)
		go s.retransmitLoop(runCtx)
	} else {
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			cancel()
			s.started.Store(false)
			return fmt.Errorf("coap listen %s: %w", s.opts.Addr, err)
		}
		s.udpConn = conn
		s.wg.Add(4)
		go s.readLoop(runCtx)
		go s.sweepLoop(runCtx)
		go s.seenCleanupLoop(runCtx)
		go s.retransmitLoop(runCtx)
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

// dtlsReadBufferSize returns the per-DTLS-connection read buffer size,
// falling back to MaxDatagram when the option is unset (zero value).
func (s *Server) dtlsReadBufferSize() int {
	if s.opts.DTLSReadBufferBytes > 0 {
		return s.opts.DTLSReadBufferBytes
	}
	return s.opts.MaxDatagram
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
	// OnClose only fires for sessions that were actually accepted; on Register
	// or OnAccept failure the plugin chain already rolled back partial accepts.
	accepted := false
	defer func() {
		s.dtlsConns.Delete(id)
		// Guarded by LoadAndDelete so OnClose fires exactly once even when
		// CloseSessions already removed this session via closeSession.
		if _, loaded := s.sessions.LoadAndDelete(id); loaded {
			s.rt.Sessions().Unregister(id)
			if accepted {
				s.rt.Plugins().OnClose(sess)
			}
		}
	}()

	if err := s.rt.Sessions().Register(sess); err != nil {
		_ = sess.Close(context.Background())
		return
	}
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		_ = sess.Close(context.Background())
		return
	}
	accepted = true

	buf := make([]byte, s.dtlsReadBufferSize())
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// Anti-slowloris: a DTLS peer that goes silent for SessionTTL is
		// closed instead of holding a goroutine/session forever.
		if s.opts.SessionTTL > 0 {
			if err := conn.SetReadDeadline(time.Now().Add(s.opts.SessionTTL)); err != nil {
				return
			}
		}
		n, err := conn.Read(buf)
		if err != nil {
			// A read-deadline expiry means the peer went silent (half-open
			// DTLS); count the reclaim.
			if shared.IsTimeout(err) {
				s.rt.Metrics().IncCounter("sessions_reclaimed_total")
			}
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
	// RFC 7252 deduplication applies to CON messages only. Only CON messages
	// are recorded, so a NON/ACK/RST carrying the same msgID cannot later
	// cause a legitimate CON request to be misclassified as a duplicate.
	if msg.Type == TypeCON {
		sk := seenKey{remote: sess.remote.String(), msgID: msg.MessageID}
		if _, loaded := s.seen.LoadOrStore(sk, struct{}{}); loaded {
			if err := s.sendACK(sess, msg, CodeValid, nil); err != nil && s.rt != nil {
				s.rt.Logger().Warn("coap dedup ack send failed", "error", err)
			}
			return
		}
	}
	if msg.Type == TypeACK {
		// An ACK acknowledges a CON notification (or a piggybacked response);
		// clear the pending retransmission for this remote + msgID.
		s.clearPendingNotify(sess.remote.String(), msg.MessageID)
		return
	}
	if msg.Type == TypeRST {
		// RST rejects a CON notification; stop retransmitting it.
		s.clearPendingNotify(sess.remote.String(), msg.MessageID)
		return
	}

	registeredObserve := s.handleObserve(sess, msg)

	payload, err := s.rt.Plugins().OnMessage(sess, msg.Payload)
	if err != nil {
		if err != core.ErrPluginDrop {
			// Remove the session so the peer is not wedged to a closed
			// session. DTLS sessions self-heal via their handler defer
			// (closing the conn unblocks the read loop); plain UDP peers
			// are keyed by address and must be removed explicitly.
			if sess.dtlsConn != nil {
				_ = sess.Close(context.Background())
			} else {
				s.closeSession(context.Background(), sess.remote.String(), sess)
			}
		}
		if msg.Type == TypeCON {
			if err := s.sendACK(sess, msg, CodeInternalServerError, nil); err != nil && s.rt != nil {
				s.rt.Logger().Warn("coap error ack send failed", "error", err)
			}
		}
		return
	}
	var responsePayload []byte
	if s.opts.Responder != nil {
		handlerMsg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolCoAP, Payload: payload}
		err = shared.CallHandler(func() (e error) {
			responsePayload, e = s.opts.Responder(sess, handlerMsg)
			return e
		}, s.rt.Logger())
		if err != nil {
			if sess.dtlsConn != nil {
				_ = sess.Close(context.Background())
			} else {
				s.closeSession(context.Background(), sess.remote.String(), sess)
			}
			return
		}
	} else if s.opts.Handler != nil {
		handlerMsg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolCoAP, Payload: payload}
		if err := shared.CallHandler(func() error { return s.opts.Handler(sess, handlerMsg) }, s.rt.Logger()); err != nil {
			if sess.dtlsConn != nil {
				_ = sess.Close(context.Background())
			} else {
				s.closeSession(context.Background(), sess.remote.String(), sess)
			}
		}
	}
	if msg.Type == TypeCON {
		resp := ACK(msg, responseCode(msg.Code), responsePayload)
		s.addObserveSeq(sess, msg, &resp)
		if err := s.sendACKMsg(sess, resp); err != nil && s.rt != nil {
			s.rt.Logger().Warn("coap ack send failed", "error", err)
		}
	} else if msg.Type == TypeNON && registeredObserve {
		// RFC 7641: a NON observe registration receives the current value as
		// the initial notification (there is no ACK for NON messages).
		seq, ok := s.observerSeq(sess, msg)
		if !ok {
			seq = 0
		}
		notify := Message{
			Type:      TypeNON,
			Code:      CodeContent,
			MessageID: msg.MessageID,
			Token:     msg.Token,
			Options:   map[uint16][]byte{ObserveOption: encodeObserveSeq(seq)},
			Payload:   responsePayload,
		}
		if data, err := notify.Marshal(); err == nil {
			if err := sess.Send(data); err != nil && s.rt != nil {
				s.rt.Logger().Warn("coap observe initial notify send failed", "error", err)
			}
		}
	}
}

// handleObserve registers or removes an observer per the Observe option, and
// reports whether a new observer was registered (so callers can send the
// initial notification for NON messages, which have no ACK to carry it).
func (s *Server) handleObserve(sess *session, msg Message) bool {
	if msg.Code != CodeGet {
		return false
	}
	obsVal, hasObserve := msg.Options[ObserveOption]
	resource := string(msg.Payload)
	remote := sess.remote.String()
	if hasObserve && len(obsVal) > 0 {
		reg := obsVal[0]
		if reg == 0 {
			s.observers.Register(resource, remote, msg.Token)
			return true
		} else if reg == 1 {
			s.observers.Remove(resource, remote, msg.Token)
		}
	}
	return false
}

// observerSeq returns the observer's next sequence number for a GET request,
// or false if the request has no registered observer.
func (s *Server) observerSeq(sess *session, req Message) (uint32, bool) {
	if req.Code != CodeGet {
		return 0, false
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
	if obs == nil {
		return 0, false
	}
	return obs.NextSeq(), true
}

func (s *Server) addObserveSeq(sess *session, req Message, resp *Message) {
	seq, ok := s.observerSeq(sess, req)
	if !ok {
		return
	}
	if resp.Options == nil {
		resp.Options = make(map[uint16][]byte)
	}
	resp.Options[ObserveOption] = encodeObserveSeq(seq)
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
		notify := Message{
			Type:      TypeCON,
			Code:      CodeContent,
			MessageID: s.nextMessageID(),
			Token:     obs.Token,
			Options:   map[uint16][]byte{ObserveOption: encodeObserveSeq(seq)},
			Payload:   payload,
		}
		data, err := notify.Marshal()
		if err != nil {
			continue
		}
		// Send first, then track: the retransmit loop must never precede the
		// initial send (which would deliver a duplicate before the peer ever
		// received the notification). The notification is tracked until it is
		// ACKed (RFC 7641 §4.2): a lost CON notification is resent, not
		// silently dropped.
		if err := s.sendACKMsg(sess, notify); err != nil {
			s.observers.Remove(resource, obs.Remote, obs.Token)
			continue
		}
		s.trackNotify(obs.Remote, notify.MessageID, data)
	}
}

func (s *Server) trackNotify(remote string, msgID uint16, data []byte) {
	s.retransmitMu.Lock()
	s.pendingNotifies[notifyKey{remote: remote, msgID: msgID}] = pendingNotify{data: data}
	s.retransmitMu.Unlock()
}

func (s *Server) untrackNotify(remote string, msgID uint16) {
	s.retransmitMu.Lock()
	delete(s.pendingNotifies, notifyKey{remote: remote, msgID: msgID})
	s.retransmitMu.Unlock()
}

func (s *Server) clearPendingNotify(remote string, msgID uint16) {
	s.untrackNotify(remote, msgID)
}

func (s *Server) retransmitLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(retransmitInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.retransmitDue()
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) retransmitDue() {
	// Snapshot the due entries under the lock, then send outside it so a
	// stalled observer's blocking Write cannot stall ACK/RST clearing or every
	// other observer's retransmission.
	s.retransmitMu.Lock()
	due := make([]notifyKey, 0, len(s.pendingNotifies))
	for key, pn := range s.pendingNotifies {
		if pn.attempts >= maxRetransmit {
			delete(s.pendingNotifies, key)
			continue
		}
		due = append(due, key)
	}
	s.retransmitMu.Unlock()

	for _, key := range due {
		s.retransmitMu.Lock()
		pn, ok := s.pendingNotifies[key]
		if !ok {
			s.retransmitMu.Unlock()
			continue
		}
		pn.attempts++
		s.pendingNotifies[key] = pn
		data := pn.data
		remote := key.remote
		s.retransmitMu.Unlock()

		sess := s.findSessionByRemote(remote)
		if sess == nil {
			s.untrackNotify(remote, key.msgID)
			continue
		}
		if err := sess.Send(data); err != nil {
			s.untrackNotify(remote, key.msgID)
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
					// Count only if the sweep actually removed the session, so
					// a DTLS session already reclaimed by its read deadline is
					// not double-counted.
					if s.closeSession(context.Background(), key, sess) {
						s.rt.Metrics().IncCounter("sessions_reclaimed_total")
					}
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
	// Allocate the ID up front so the published session never carries a
	// provisional id=0 that a concurrent sweep/close could observe (a data
	// race on sess.id). Wasting an ID on a duplicate race is harmless.
	sess := newUDPSession(s.rt.Sessions().NextID(), s.udpConn, addr)
	actual, loaded := s.sessions.LoadOrStore(key, sess)
	if loaded {
		_ = sess.Close(context.Background())
		return actual.(*session)
	}
	if err := s.rt.Sessions().Register(sess); err != nil {
		s.sessions.Delete(key)
		_ = sess.Close(context.Background())
		return nil
	}
	if err := s.rt.Plugins().OnAccept(sess); err != nil {
		// OnAccept failed; the plugin chain already rolled back partial
		// accepts, so remove the session without calling OnClose again.
		if _, loaded := s.sessions.LoadAndDelete(key); loaded {
			s.rt.Sessions().Unregister(sess.ID())
			_ = sess.Close(context.Background())
		}
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

func (s *Server) closeSession(ctx context.Context, key any, sess *session) bool {
	if _, loaded := s.sessions.LoadAndDelete(key); !loaded {
		return false
	}
	s.observers.RemoveBySession(sess.remote.String())
	s.rt.Sessions().Unregister(sess.ID())
	_ = sess.Close(ctx)
	s.rt.Plugins().OnClose(sess)
	return true
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
