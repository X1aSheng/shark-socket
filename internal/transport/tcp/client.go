package tcp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type Client struct {
	addr      string
	framer    Framer
	tlsConfig *tls.Config
	dialer    net.Dialer
	conn      net.Conn
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

func (c *Client) Connect(ctx context.Context) error {
	conn, err := c.dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return fmt.Errorf("tcp client dial %s: %w", c.addr, err)
	}
	if c.tlsConfig != nil {
		tlsConn := tls.Client(conn, c.tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return fmt.Errorf("tcp client tls handshake: %w", err)
		}
		conn = tlsConn
	}
	c.conn = conn
	return nil
}

func (c *Client) Send(payload []byte) error {
	if c.conn == nil {
		return core.ErrClosed
	}
	return c.framer.WriteFrame(c.conn, payload)
}

func (c *Client) Receive() ([]byte, error) {
	if c.conn == nil {
		return nil, core.ErrClosed
	}
	return c.framer.ReadFrame(c.conn)
}

func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
