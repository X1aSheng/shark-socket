package tcp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// Client is a framed TCP client. It is safe for concurrent use: Connect,
// Send, Receive and Close may be called from different goroutines. Calls that
// race a Connect observe either the previous or the new connection.
type Client struct {
	addr        string
	framer      Framer
	tlsConfig   *tls.Config
	dialer      net.Dialer
	linger      *int // SO_LINGER timeout; nil means OS default
	readTimeout time.Duration
	// mu guards conn. It is held only for the pointer swap, never across I/O,
	// so a slow dial or a blocking write does not stall other callers.
	mu   sync.RWMutex
	conn net.Conn
}

type ClientOption func(*Client)

func NewClient(addr string, opts ...ClientOption) *Client {
	c := &Client{
		addr:   addr,
		framer: LengthPrefixFramer{MaxFrameBytes: 1024 * 1024},
		dialer: net.Dialer{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func WithClientFramer(framer Framer) ClientOption {
	return func(c *Client) {
		if framer != nil {
			c.framer = framer
		}
	}
}

func WithClientTLS(config *tls.Config) ClientOption {
	return func(c *Client) {
		c.tlsConfig = config
	}
}

// WithClientLinger sets SO_LINGER on the underlying TCP connection.
// A zero timeout causes an RST on close instead of FIN, avoiding
// TIME_WAIT on the client side — useful for benchmarks to prevent
// ephemeral port exhaustion.
func WithClientLinger(sec int) ClientOption {
	return func(c *Client) {
		c.linger = &sec
	}
}

func (c *Client) Connect(ctx context.Context) error {
	conn, err := c.dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return fmt.Errorf("tcp client dial %s: %w", c.addr, err)
	}
	if c.linger != nil {
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetLinger(*c.linger)
		}
	}
	if c.tlsConfig != nil {
		tlsConn := tls.Client(conn, c.tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return fmt.Errorf("tcp client tls handshake: %w", err)
		}
		conn = tlsConn
	}
	// Install the new connection and release the previous one. Dialing happens
	// outside the lock so a slow dial cannot block Send/Receive/Close, and the
	// old connection is closed after the swap so a concurrent caller holding a
	// snapshot of it is not left with a closed socket mid-operation.
	c.mu.Lock()
	prev := c.conn
	c.conn = conn
	c.mu.Unlock()
	if prev != nil {
		_ = prev.Close()
	}
	return nil
}

// currentConn returns the active connection, or core.ErrClosed when the client
// is not connected.
func (c *Client) currentConn() (net.Conn, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.conn == nil {
		return nil, core.ErrClosed
	}
	return c.conn, nil
}

// currentConnOrNil reports the active connection for tests.
func (c *Client) currentConnOrNil() net.Conn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}

func (c *Client) Send(payload []byte) error {
	conn, err := c.currentConn()
	if err != nil {
		return err
	}
	return c.framer.WriteFrame(conn, payload)
}

func (c *Client) Receive() ([]byte, error) {
	conn, err := c.currentConn()
	if err != nil {
		return nil, err
	}
	// Apply the configured read timeout so a half-dead peer does not block
	// Receive forever (WithClientReadTimeout).
	if c.readTimeout > 0 {
		if err := conn.SetReadDeadline(time.Now().Add(c.readTimeout)); err != nil {
			return nil, err
		}
	}
	return c.framer.ReadFrame(conn)
}

// WithClientReadTimeout sets a per-Receive read deadline (0 disables).
func WithClientReadTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.readTimeout = timeout
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}
