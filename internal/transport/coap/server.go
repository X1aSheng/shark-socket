package coap

import (
	"context"
	"errors"
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

// pendingNotify tracks an unacknowledged CON message for retransmission
// (RFC 7252 section 4.2 reliability). token is the observe relation token
// carried by a notification, used to cancel the relation when the peer never
// acknowledges.
type pendingNotify struct {
	data  []byte
	token []byte
	// attempts counts transmissions after the first one, so 0 means "sent
	// once, never retransmitted".
	attempts int
	// timeout is the interval that must elapse before the next transmission.
	// It starts in [2s, 3s) and doubles on every retransmission.
	timeout time.Duration
	// deadline is the absolute time of the next transmission. It is advanced
	// from itself rather than from "now" so scheduler latency does not
	// accumulate across retransmissions.
	deadline time.Time
}

// seenEntry is the deduplication record for one CON exchange (RFC 7252
// section 4.5). resp holds the exact bytes of the piggybacked ACK once they
// exist, so a retransmitted request receives the same response instead of
// re-running the handler.
type seenEntry struct {
	mu   sync.Mutex
	resp []byte
}

func (e *seenEntry) response() []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resp
}

func (e *seenEntry) setResponse(data []byte) {
	e.mu.Lock()
	e.resp = data
	e.mu.Unlock()
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
	// quota bounds the session table. Nil when both limits are unlimited.
	quota           *shared.PeerQuota
	seen            sync.Map
	observers       *ObserverRegistry
	retransmitMu    sync.Mutex
	pendingNotifies map[notifyKey]pendingNotify
	// jitter draws the initial retransmission timeout. Nil means the real
	// random source; tests set it to pin the schedule.
	jitter func() time.Duration
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
	if s.opts.MaxSessions > 0 || s.opts.MaxSessionsPerPeer > 0 {
		s.quota = shared.NewPeerQuota(s.opts.MaxSessions, s.opts.MaxSessionsPerPeer)
	}
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
	// A listener that keeps failing immediately (EMFILE, a listener left in a
	// bad state, a peer flooding malformed handshakes) would spin this loop at
	// 100% CPU. Back off between consecutive failures.
	backoff := shared.NewBackoff(shared.DefaultBackoffMin, shared.DefaultBackoffMax)
	for {
		conn, err := s.dtlsLn.Accept()
		if err != nil {
			// A closed listener (including one closed behind our back) is
			// terminal: retrying can never succeed.
			if s.closed.Load() || ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			if !backoff.Wait(ctx) {
				return
			}
			continue
		}
		backoff.Reset()
		// Enforce the configured client-authentication policy ourselves:
		// pion/dtls v3 does not reliably apply it on every handshake path.
		// The peer cannot exchange application data before Accept returns, so
		// closing here is equivalent to rejecting the handshake.
		if err := shared.EnforceServerClientAuth(s.opts.TLSConfig, conn); err != nil {
			_ = conn.Close()
			if s.rt != nil {
				s.rt.Logger().Warn("coap dtls client auth rejected", "error", err)
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
		// Release the DTLS socket. The read loop returns on any read error
		// (peer disconnect, read deadline, transport failure), and deleting
		// the entry from dtlsConns below also hides it from StopAccept - so
		// without this close every departing DTLS peer would leak its socket
		// until the finalizer happened to run. Close is idempotent
		// (closeOnce), so a concurrent CloseSessions/closeSession is safe.
		_ = sess.Close(context.Background())
		s.dtlsConns.Delete(id)
		// The peer's transport session has ended (disconnect, read deadline,
		// or shutdown); drop any observe relations it held so they cannot
		// linger until the next notification. Idempotent: closeSession also
		// performs this cleanup when it runs first.
		s.observers.RemoveBySession(sess.remote.String())
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
	// A socket that returns an error without the server being closed (e.g. an
	// out-of-band close, a transient OS-level failure) must not be retried in a
	// tight loop; back off like the accept loop does.
	backoff := shared.NewBackoff(shared.DefaultBackoffMin, shared.DefaultBackoffMax)
	for {
		n, addr, err := s.udpConn.ReadFromUDP(buf)
		if err != nil {
			// A closed socket (including one closed behind our back) is
			// terminal: retrying can never succeed.
			if s.closed.Load() || ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			if !backoff.Wait(ctx) {
				return
			}
			continue
		}
		backoff.Reset()
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
	remote := sess.remote.String()
	// ACK and RST are Empty messages (RFC 7252 §4.2) and are handled before
	// deduplication: neither is a CON, so ordering does not change the dedup
	// outcome, and an empty CON ping must not pollute the dedup table.
	if msg.Type == TypeACK {
		// An ACK acknowledges a CON notification (or a piggybacked response);
		// clear the pending retransmission for this remote + msgID.
		s.clearPendingNotify(remote, msg.MessageID)
		return
	}
	if msg.Type == TypeRST {
		// RST rejects a CON notification: stop retransmitting it and cancel
		// the observe relation the rejected notification refers to
		// (RFC 7641 §4.4). A conformant RST is an Empty message that carries
		// no token (RFC 7252 §4.1/§4.2), so the relation is identified via
		// the token stored with the pending notification, keyed by the RST
		// Message ID.
		token, _ := s.takePendingNotify(remote, msg.MessageID)
		if len(token) > 0 {
			s.observers.RemoveByToken(remote, token)
		}
		return
	}
	// A CON with code 0.00 is a CoAP ping and must be answered with an RST
	// (RFC 7252 §4.3); any other Empty message is silently ignored — it has
	// no application-layer semantics.
	if msg.Code == CodeEmpty {
		if msg.Type == TypeCON {
			rst := Message{Type: TypeRST, Code: CodeEmpty, MessageID: msg.MessageID}
			if err := s.sendACKMsg(sess, rst); err != nil && s.rt != nil {
				s.rt.Logger().Warn("coap ping rst send failed", "error", err)
			}
		}
		return
	}
	// RFC 7252 deduplication applies to CON messages only. Only CON messages
	// are recorded, so a NON/ACK/RST carrying the same msgID cannot later
	// cause a legitimate CON request to be misclassified as a duplicate.
	var dedup *seenEntry
	if msg.Type == TypeCON {
		sk := seenKey{remote: remote, msgID: msg.MessageID}
		entry := &seenEntry{}
		if actual, loaded := s.seen.LoadOrStore(sk, entry); loaded {
			s.replyToDuplicate(sess, msg, actual.(*seenEntry))
			return
		}
		dedup = entry
	}

	// Block1 (RFC 7959) is not implemented: a request body arriving in blocks
	// would otherwise be executed once per block, with the handler seeing a
	// fragment as if it were the whole body - for a LwM2M write that is a
	// correctness bug with side effects. Refuse explicitly with 4.13 and a
	// Block1 hint, which is how RFC 7959 section 2.9.3 tells a client to stop
	// using block-wise uploads against this server.
	if hasBlock1(msg.Options) {
		s.replyBlock1Unsupported(sess, msg, dedup)
		return
	}

	registeredObserve := s.handleObserve(sess, msg)

	payload, err := s.rt.Plugins().OnMessage(sess, msg.Payload)
	if err != nil {
		if !errors.Is(err, core.ErrPluginDrop) {
			// Remove the session so the peer is not wedged to a closed
			// session. DTLS sessions self-heal via their handler defer
			// (closing the conn unblocks the read loop); plain UDP peers
			// are keyed by address and must be removed explicitly.
			if sess.dtlsConn != nil {
				_ = sess.Close(context.Background())
			} else {
				s.closeSession(context.Background(), sess.key, sess)
			}
		}
		if msg.Type == TypeCON {
			if err := s.sendDedupResponse(sess, msg, CodeInternalServerError, nil, dedup, responseOptions{}); err != nil && s.rt != nil {
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
				s.closeSession(context.Background(), sess.key, sess)
			}
			return
		}
	} else if s.opts.Handler != nil {
		handlerMsg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolCoAP, Payload: payload}
		if err := shared.CallHandler(func() error { return s.opts.Handler(sess, handlerMsg) }, s.rt.Logger()); err != nil {
			if sess.dtlsConn != nil {
				_ = sess.Close(context.Background())
			} else {
				s.closeSession(context.Background(), sess.key, sess)
			}
		}
	}
	if msg.Type == TypeCON {
		code := responseCode(msg.Code)
		body, blockOpt, ok := s.applyBlock2(msg, responsePayload)
		if !ok {
			// A Block2 request for a block past the end of the body, or an
			// undecodable option: 4.02 Bad Option (RFC 7959 section 2.9.2).
			code, body, blockOpt = CodeBadOption, nil, nil
		}
		if err := s.sendDedupResponse(sess, msg, code, body, dedup, responseOptions{observe: true, block2: blockOpt}); err != nil && s.rt != nil {
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
	if hasObserve {
		// The Observe value is a CoAP uint option encoded in the shortest
		// form, so register (Observe=0) is a zero-length option value
		// (RFC 7252 §3.2, RFC 7641 §2). Treating "absent value" and "value
		// zero" separately is required for interop with conformant clients.
		var reg byte
		if len(obsVal) > 0 {
			reg = obsVal[0]
		}
		if reg == 0 {
			// Register returns nil when subscription limits are reached; the
			// request is then answered without an observe relation instead of
			// unboundedly growing the registry.
			return s.observers.Register(resource, remote, msg.Token) != nil
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
		s.trackNotify(time.Now(), obs.Remote, notify.MessageID, notify.Token, data)
	}
}

func (s *Server) trackNotify(now time.Time, remote string, msgID uint16, token []byte, data []byte) {
	timeout := s.initialTimeout()
	s.retransmitMu.Lock()
	s.pendingNotifies[notifyKey{remote: remote, msgID: msgID}] = pendingNotify{
		data:     data,
		token:    append([]byte(nil), token...),
		attempts: 0,
		timeout:  timeout,
		deadline: now.Add(timeout),
	}
	s.retransmitMu.Unlock()
}

// initialTimeout draws the first retransmission timeout for one message. The
// jitter field exists so a test can pin it; production always uses the random
// source, because a fixed interval makes every device that loses connectivity
// at the same moment retransmit in lockstep.
func (s *Server) initialTimeout() time.Duration {
	if s.jitter != nil {
		return initialRetransmitTimeout(s.jitter())
	}
	return initialRetransmitTimeout(defaultJitter())
}

func (s *Server) untrackNotify(remote string, msgID uint16) {
	s.retransmitMu.Lock()
	delete(s.pendingNotifies, notifyKey{remote: remote, msgID: msgID})
	s.retransmitMu.Unlock()
}

func (s *Server) clearPendingNotify(remote string, msgID uint16) {
	s.untrackNotify(remote, msgID)
}

// takePendingNotify removes and returns the token of a pending CON
// notification identified by remote + msgID. It is used to correlate a
// conformant (token-less) RST with the observe relation it rejects.
func (s *Server) takePendingNotify(remote string, msgID uint16) ([]byte, bool) {
	s.retransmitMu.Lock()
	defer s.retransmitMu.Unlock()
	key := notifyKey{remote: remote, msgID: msgID}
	pn, ok := s.pendingNotifies[key]
	if !ok {
		return nil, false
	}
	delete(s.pendingNotifies, key)
	return pn.token, true
}

func (s *Server) retransmitLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(retransmitTick)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.retransmitDue(time.Now())
		case <-ctx.Done():
			return
		}
	}
}

// retransmitDue transmits every pending entry whose own deadline has elapsed.
//
// The previous implementation retransmitted everything on every 2s tick, which
// violated RFC 7252 section 4.2 twice over: an entry tracked just before a tick
// was retransmitted after a few milliseconds instead of ACK_TIMEOUT, and the
// interval never doubled. now is a parameter so the schedule can be exercised
// without waiting for real time.
func (s *Server) retransmitDue(now time.Time) {
	// Snapshot the due entries under the lock, then send outside it so a
	// stalled observer's blocking Write cannot stall ACK/RST clearing or every
	// other observer's retransmission.
	s.retransmitMu.Lock()
	due := make([]notifyKey, 0, len(s.pendingNotifies))
	var abandoned []struct {
		remote string
		token  []byte
	}
	for key, pn := range s.pendingNotifies {
		switch pn.decide(now) {
		case retransmitWait:
			continue
		case retransmitGiveUp:
			delete(s.pendingNotifies, key)
			// The peer never acknowledged the message: cancel the observe
			// relation so a dead or unreachable client cannot keep a
			// subscription (and its notification stream) alive forever.
			abandoned = append(abandoned, struct {
				remote string
				token  []byte
			}{remote: key.remote, token: pn.token})
		case retransmitSend:
			due = append(due, key)
		}
	}
	s.retransmitMu.Unlock()

	for _, key := range due {
		s.retransmitMu.Lock()
		pn, ok := s.pendingNotifies[key]
		if !ok {
			s.retransmitMu.Unlock()
			continue
		}
		pn = pn.advance(now)
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
	// Cancel relations whose CON notifications were never acknowledged: the
	// client is unreachable, so keeping the relation would only produce more
	// notifications for a peer that cannot receive them.
	for _, abandoned := range abandoned {
		s.observers.RemoveByToken(abandoned.remote, abandoned.token)
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
				// Sessions that still hold observe relations belong to clients
				// that may legitimately be silent between notifications (the
				// next notification is due less often than SessionTTL). Idle
				// sweeping them would silently drop live subscriptions, so
				// they are exempt; relations end via RST (RFC 7641 §4.4),
				// retransmission exhaustion or session teardown instead.
				if s.observers.HasObservers(sess.remote.String()) {
					return true
				}
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
// CoAP MessageIDs are 16-bit (wrapping at 65536), so a 5-minute interval is safe:
// it exceeds EXCHANGE_LIFETIME (247s, RFC 7252 section 4.8.2), which is how long
// a peer may still retransmit a request whose response is cached here.
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
	// shared.PeerKey instead of addr.String(): every datagram resolves its
	// session here. See internal/transport/shared/peerkey.go.
	key := shared.NewPeerKey(addr)
	if value, ok := s.sessions.Load(key); ok {
		sess := value.(*session)
		// A plugin or error path may have Closed this session while the peer
		// keeps sending (e.g. AutoBan kill, handler failure). Such a session
		// can never send again and would otherwise linger here, refreshed by
		// every datagram via touch(), until the idle sweep eventually gives up
		// — retaining a dead slot and a stale session ID. Reap it (removing
		// any observers it registered) and let the next packet create a fresh
		// session, which re-runs OnAccept so gating plugins still apply.
		if sess.State() == core.StateClosed {
			s.closeSession(context.Background(), key, sess)
		} else {
			return sess
		}
	}
	// Admission control before anything is allocated. See the UDP transport for
	// why the ceiling has to sit here rather than on the SessionManager: by the
	// time the manager refuses, every legitimate peer is refused too.
	ip := shared.PeerIP(addr)
	if s.quota != nil && !s.quota.TryAcquire(ip) {
		s.rt.Metrics().IncCounter("sessions_rejected_total")
		return nil
	}
	// Allocate the ID up front so the published session never carries a
	// provisional id=0 that a concurrent sweep/close could observe (a data
	// race on sess.id). Wasting an ID on a duplicate race is harmless.
	sess := newUDPSession(s.rt.Sessions().NextID(), s.udpConn, addr)
	if s.quota != nil {
		// The slot is returned by session.Close, which every exit path below
		// already calls exactly once.
		sess.release = func() { s.quota.Release(ip) }
	}
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

// responseOptions are the decorations a caller may attach to a piggybacked ACK.
// They are a struct rather than more parameters because the set grows: Observe
// and Block2 already coexist on the first block of an observed resource.
type responseOptions struct {
	// observe appends the Observe sequence number for a registered relation.
	observe bool
	// block2 attaches the Block2 option describing the slice being sent. Nil
	// means "no block option", which is what a body that fits in one datagram
	// gets.
	block2 *BlockOption
}

// sendDedupResponse sends the piggybacked ACK for a CON request and records the
// exact bytes in the exchange's dedup entry, so a retransmitted request is
// answered with the same response instead of running the handler again
// (RFC 7252 section 4.5).
func (s *Server) sendDedupResponse(sess *session, req Message, code byte, payload []byte, dedup *seenEntry, opts responseOptions) error {
	resp := ACK(req, code, payload)
	if opts.observe {
		s.addObserveSeq(sess, req, &resp)
	}
	if opts.block2 != nil {
		if resp.Options == nil {
			resp.Options = make(map[uint16][]byte, 1)
		}
		resp.Options[Block2Option] = opts.block2.Marshal()
	}
	data, err := resp.Marshal()
	if err != nil {
		return err
	}
	if dedup != nil {
		dedup.setResponse(data)
	}
	return sess.Send(data)
}

// applyBlock2 slices the response body into the block the peer asked for.
// ok is false when the request carries an undecodable Block2 option or asks for
// a block past the end of the body; both are answered with 4.02 Bad Option.
//
// Block-wise is stateless here: the body is regenerated by the responder for
// every block request instead of being cached between them. Caching would keep
// an arbitrarily large buffer per in-flight transfer, and unbounded per-peer
// buffers are exactly the resource-exhaustion shape ADR-017 removed from the
// session table.
func (s *Server) applyBlock2(req Message, body []byte) ([]byte, *BlockOption, bool) {
	if s.opts.MaxBlockSize <= 0 {
		return body, nil, true
	}
	blockReq, err := negotiateBlock2(req.Options, s.opts.MaxBlockSize)
	if err != nil {
		return nil, nil, false
	}
	return blockFor(body, blockReq)
}

// hasBlock1 reports whether the request asks to upload its body in blocks.
func hasBlock1(opts map[uint16][]byte) bool {
	_, ok := opts[Block1Option]
	return ok
}

// replyBlock1Unsupported refuses a block-wise upload: RFC 7959 section 2.9.3
// says the server answers 4.13 Request Entity Too Large and offers a Block1
// option so the client learns which size it may use - except that this server
// accepts no block-wise uploads at all, so the hint is SZX 0 (the smallest
// block) and the client's next move is to send the body in one request.
func (s *Server) replyBlock1Unsupported(sess *session, req Message, dedup *seenEntry) {
	hint := BlockOption{Num: 0, More: false, SizeExp: minBlockSizeExp}
	resp := ACK(req, CodeRequestEntityTooLarge, nil)
	resp.Options = map[uint16][]byte{Block1Option: hint.Marshal()}
	data, err := resp.Marshal()
	if err != nil {
		return
	}
	if dedup != nil {
		dedup.setResponse(data)
	}
	if err := sess.Send(data); err != nil && s.rt != nil {
		s.rt.Logger().Warn("coap block1 refusal send failed", "error", err)
	}
}

// replyToDuplicate answers a retransmitted CON request (RFC 7252 section 4.5).
// It must never reach the handler: the first request already produced a
// response, and re-running it would repeat side effects - for an LwM2M write or
// execute that is a correctness bug, not a performance one.
func (s *Server) replyToDuplicate(sess *session, req Message, entry *seenEntry) {
	if data := entry.response(); data != nil {
		if err := sess.Send(data); err != nil && s.rt != nil {
			s.rt.Logger().Warn("coap dedup response resend failed", "error", err)
		}
		return
	}
	// The first response does not exist yet (the handler is still running, or
	// it failed without producing one). An empty ACK tells the client to stop
	// retransmitting without claiming a response code the server does not
	// have - RFC 7252 section 4.2, separate response.
	err := s.sendACKMsg(sess, Message{Type: TypeACK, Code: CodeEmpty, MessageID: req.MessageID})
	if err != nil && s.rt != nil {
		s.rt.Logger().Warn("coap dedup ack send failed", "error", err)
	}
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
