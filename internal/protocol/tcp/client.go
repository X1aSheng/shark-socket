package tcp

import (
	"crypto/rand"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Client is a TCP client with optional auto-reconnect and TLS support.
type Client struct {
	addr      string
	tlsConfig *tls.Config
	conn      net.Conn
	framer    Framer
	timeout   time.Duration
	reconnect bool
	maxRetry  int
	mu        sync.Mutex
	closed    atomic.Bool
}

// NewClient creates a new TCP client.
func NewClient(addr string, opts ...ClientOption) *Client {
	c := &Client{
		addr:     addr,
		framer:   NewLengthPrefixFramer(1024 * 1024),
		timeout:  10 * time.Second,
		maxRetry: 10,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Connect establishes a connection to the server.
func (c *Client) Connect() error {
	return c.connectWithRetry(0)
}

func (c *Client) connectWithRetry(attempt int) error {
	var conn net.Conn
	var err error

	if c.timeout > 0 {
		if c.tlsConfig != nil {
			conn, err = tls.DialWithDialer(&net.Dialer{Timeout: c.timeout}, "tcp", c.addr, c.tlsConfig)
		} else {
			conn, err = net.DialTimeout("tcp", c.addr, c.timeout)
		}
	} else {
		if c.tlsConfig != nil {
			conn, err = tls.Dial("tcp", c.addr, c.tlsConfig)
		} else {
			conn, err = net.Dial("tcp", c.addr)
		}
	}

	if err != nil {
		if !c.reconnect || attempt >= c.maxRetry || c.closed.Load() {
			return err
		}
		time.Sleep(jitterBackoff(attempt))
		return c.connectWithRetry(attempt + 1)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	return nil
}

// Send writes a framed message. Auto-reconnects if enabled.
func (c *Client) Send(data []byte) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return errNotConnected
	}
	return c.sendWithReconnect(conn, data)
}

func (c *Client) sendWithReconnect(conn net.Conn, data []byte) error {
	err := c.framer.WriteFrame(conn, data)
	if err == nil || !c.reconnect {
		return err
	}
	if reconnErr := c.Connect(); reconnErr != nil {
		return reconnErr
	}
	c.mu.Lock()
	conn = c.conn
	c.mu.Unlock()
	return c.framer.WriteFrame(conn, data)
}

// Receive reads a framed message.
func (c *Client) Receive() ([]byte, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil, errNotConnected
	}
	return c.framer.ReadFrame(conn)
}

// SendReceive sends a message and waits for the response.
func (c *Client) SendReceive(data []byte) ([]byte, error) {
	if err := c.Send(data); err != nil {
		return nil, err
	}
	return c.Receive()
}

// Close closes the connection.
func (c *Client) Close() error {
	c.closed.Store(true)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// IsConnected returns true if the client has an active connection.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// RemoteAddr returns the remote address or nil if not connected.
func (c *Client) RemoteAddr() net.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	return c.conn.RemoteAddr()
}

// ClientOption configures a TCP client.
type ClientOption func(*Client)

// WithClientTLS sets TLS configuration.
func WithClientTLS(cfg *tls.Config) ClientOption {
	return func(c *Client) { c.tlsConfig = cfg }
}

// WithClientTimeout sets connection timeout.
func WithClientTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.timeout = d }
}

// WithClientFramer sets the framer.
func WithClientFramer(f Framer) ClientOption {
	return func(c *Client) { c.framer = f }
}

// WithClientReconnect enables auto-reconnect with max retries.
func WithClientReconnect(enable bool, maxRetry int) ClientOption {
	return func(c *Client) { c.reconnect = enable; c.maxRetry = maxRetry }
}

// jitterBackoff computes exponential backoff with ±20% jitter.
func jitterBackoff(attempt int) time.Duration {
	base := 100 * time.Millisecond
	d := base << min(attempt, 14) // max ~1.6s at attempt 14
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	// Add jitter: ±20%
	var jitterBytes [8]byte
	_, _ = rand.Read(jitterBytes[:])
	jitterFactor := float64(jitterBytes[0]) / 255.0 // 0.0 to 1.0
	jitter := time.Duration(float64(d) * 0.2 * (jitterFactor - 0.5))
	return d + jitter
}

var errNotConnected = errors.New("tcp client: not connected")
