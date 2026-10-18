// Command netpoll is the cloudwego/netpoll arm: a fixed-size byte echo.
//
// netpoll hands OnRequest a connection whose reader holds whatever arrived, so
// the echo is a nocopy Next + WriteBinary -- the counterpart of gnet's
// Peek/Next and of shark-socket's framer handing a payload to the handler.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"runtime"

	"github.com/cloudwego/netpoll"
)

func onRequest(ctx context.Context, conn netpoll.Connection) error {
	reader := conn.Reader()
	buf, err := reader.Next(reader.Len())
	if err != nil {
		return err
	}
	if len(buf) == 0 {
		return nil
	}
	// buf is only valid until Release; WriteBinary copies it into the outbound
	// buffer, which is where the other two arms put their bytes as well.
	if _, err := conn.Writer().WriteBinary(buf); err != nil {
		return err
	}
	return reader.Release()
}

func main() {
	addr := flag.String("addr", "127.0.0.1:19003", "listen address")
	flag.Parse()

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	loop, err := netpoll.NewEventLoop(onRequest)
	if err != nil {
		log.Fatalf("event loop: %v", err)
	}
	// netpoll v0.7.5 has no Windows implementation: netpoll_windows.go returns
	// (nil, nil) from NewEventLoop, so Serve would dereference nil. This bench
	// is Linux-only by design -- the same-host comparison has to run where all
	// three arms have a real event loop.
	if loop == nil {
		log.Fatalf("netpoll has no implementation on %s; run this arm on Linux", runtime.GOOS)
	}
	log.Printf("netpoll echo on %s", *addr)
	if err := loop.Serve(listener); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
