package http

import (
	"net"
	"net/http"
	"sync"

	"github.com/yourname/shark-socket/internal/errs"
	"github.com/yourname/shark-socket/internal/session"
	"github.com/yourname/shark-socket/internal/types"
)

// HTTPSession is a per-request session for mode B (optional plugin integration).
type HTTPSession struct {
	*session.BaseSession
	w    http.ResponseWriter
	r    *http.Request
	once sync.Once
}

// NewHTTPSession creates a per-request HTTP session.
func NewHTTPSession(id uint64, w http.ResponseWriter, r *http.Request) *HTTPSession {
	remoteAddr, _ := net.ResolveTCPAddr("tcp", r.RemoteAddr)
	if remoteAddr == nil {
		remoteAddr = &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
	}
	s := &HTTPSession{
		BaseSession: session.NewBase(id, types.HTTP, remoteAddr, nilAddr()),
		w:           w,
		r:           r,
	}
	s.SetState(types.Active)
	return s
}

// Send writes data to the HTTP response writer.
func (s *HTTPSession) Send(data []byte) error {
	if !s.IsAlive() {
		return errs.ErrSessionClosed
	}
	_, err := s.w.Write(data)
	return err
}

// SendTyped encodes and sends.
func (s *HTTPSession) SendTyped(msg []byte) error {
	return s.Send(msg)
}

// Close completes the response.
func (s *HTTPSession) Close() error {
	s.once.Do(func() {
		s.SetState(types.Closed)
		s.DoClose()
	})
	return nil
}

// Request returns the underlying HTTP request.
func (s *HTTPSession) Request() *http.Request {
	return s.r
}

// ResponseWriter returns the underlying response writer.
func (s *HTTPSession) ResponseWriter() http.ResponseWriter {
	return s.w
}

func nilAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 0}
}
