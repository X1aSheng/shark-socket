package http

import (
	"context"
	"net"
	stdhttp "net/http"
)

func init() {
	stdhttp.DefaultTransport = &stdhttp.Transport{
		DialContext: dialWithLinger,
	}
}

func dialWithLinger(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetLinger(0)
	}
	return conn, nil
}
