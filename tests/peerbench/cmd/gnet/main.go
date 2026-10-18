// Command gnet is the gnet arm: a fixed-size byte echo over an event loop.
package main

import (
	"flag"
	"log"

	"github.com/panjf2000/gnet/v2"
)

type echo struct {
	gnet.BuiltinEventEngine
}

func (e *echo) OnTraffic(c gnet.Conn) gnet.Action {
	buf, err := c.Next(c.InboundBuffered())
	if err != nil {
		return gnet.Close
	}
	if len(buf) == 0 {
		return gnet.None
	}
	if _, err := c.Write(buf); err != nil {
		return gnet.Close
	}
	return gnet.None
}

func main() {
	addr := flag.String("addr", "127.0.0.1:19002", "listen address")
	multicore := flag.Bool("multicore", true, "gnet multicore mode")
	flag.Parse()

	log.Printf("gnet echo on %s (multicore=%v)", *addr, *multicore)
	if err := gnet.Run(&echo{}, "tcp://"+*addr, gnet.WithMulticore(*multicore)); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
