package lwm2m

import "time"

type Client struct {
	Endpoint string
	Lifetime time.Duration
	Objects  []ObjectPath
	server   *Server
}

type ClientOption func(*Client)

func NewClient(endpoint string, server *Server, opts ...ClientOption) *Client {
	c := &Client{Endpoint: endpoint, Lifetime: 5 * time.Minute, server: server}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func WithLifetime(lifetime time.Duration) ClientOption {
	return func(c *Client) {
		if lifetime > 0 {
			c.Lifetime = lifetime
		}
	}
}

func WithObjects(objects ...ObjectPath) ClientOption {
	return func(c *Client) {
		c.Objects = append([]ObjectPath(nil), objects...)
	}
}

func (c *Client) Register() Registration {
	return c.server.Register(c.Endpoint, c.Lifetime, c.Objects...)
}

func (c *Client) Update(lifetime time.Duration) (Registration, error) {
	if lifetime > 0 {
		c.Lifetime = lifetime
	}
	return c.server.Update(c.Endpoint, c.Lifetime)
}

func (c *Client) Deregister() {
	c.server.Deregister(c.Endpoint)
}

func (c *Client) Write(path ObjectPath, value []byte) error {
	return c.server.Write(c.Endpoint, path, value)
}

func (c *Client) Read(path ObjectPath) (Resource, bool) {
	return c.server.Read(c.Endpoint, path)
}
