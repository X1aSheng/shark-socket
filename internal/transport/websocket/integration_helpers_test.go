package websocket

import (
	"context"
	"net"
	"net/http"

	gws "github.com/gorilla/websocket"
)

func init() {
	http.DefaultTransport = &http.Transport{
		DialContext: dialWithLinger,
	}
	gws.DefaultDialer = &gws.Dialer{
		NetDialContext: dialWithLinger,
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
