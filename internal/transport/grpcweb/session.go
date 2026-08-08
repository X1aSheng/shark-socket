package grpcweb

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type session struct {
	id        uint64
	request   *http.Request
	response  http.ResponseWriter
	framed    bool
	createdAt time.Time
	activeAt  atomic.Int64
	state     atomic.Uint32
	meta      sync.Map
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func newSession(id uint64, w http.ResponseWriter, r *http.Request, framed bool) *session {
	ctx, cancel := context.WithCancel(r.Context())
	s := &session{id: id, request: r, response: w, framed: framed, createdAt: time.Now(), ctx: ctx, cancel: cancel}
	s.activeAt.Store(time.Now().UnixNano())
	s.state.Store(uint32(core.StateActive))
	return s
}

func (s *session) ID() uint64                   { return s.id }
func (s *session) Protocol() core.Protocol      { return core.ProtocolGRPCWeb }
func (s *session) RemoteAddr() net.Addr         { return stringAddr(s.request.RemoteAddr) }
func (s *session) LocalAddr() net.Addr          { return nil }
func (s *session) State() core.SessionState     { return core.SessionState(s.state.Load()) }
func (s *session) CreatedAt() time.Time         { return s.createdAt }
func (s *session) LastActiveAt() time.Time      { return time.Unix(0, s.activeAt.Load()) }
func (s *session) Context() context.Context     { return s.ctx }
func (s *session) SetMeta(k string, v any)      { s.meta.Store(k, v) }
func (s *session) GetMeta(k string) (any, bool) { return s.meta.Load(k) }
func (s *session) DelMeta(k string)             { s.meta.Delete(k) }

func (s *session) Send(payload []byte) error {
	if s.State() != core.StateActive {
		return core.ErrSessionClosed
	}
	s.response.Header().Set("content-type", "application/grpc-web+proto")
	if s.framed {
		payload = appendDataFrame(nil, payload)
	}
	_, err := s.response.Write(payload)
	return err
}

func (s *session) SendTrailers(status int, message string) error {
	if !s.framed {
		return nil
	}
	// Set the content-type here too: a response that only ever writes a
	// trailer frame (no data via Send) would otherwise be labelled text/plain
	// by net/http and rejected by the gRPC-Web client.
	s.response.Header().Set("content-type", "application/grpc-web+proto")
	_, err := s.response.Write(appendTrailerFrame(nil, status, message))
	return err
}

func (s *session) Close(context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.state.Store(uint32(core.StateClosed))
		s.cancel()
		err = nil
	})
	return err
}

type stringAddr string

func (a stringAddr) Network() string { return "grpc-web" }
func (a stringAddr) String() string  { return string(a) }

var _ core.Session = (*session)(nil)
