// Package main provides a rate-limited load client.
//
// Why a rate-limited client exists: the flood-style client used for
// saturation runs unthrottled, so on a single host it becomes the bottleneck
// and steals CPU from the server under test. That makes the measurement
// meaningless. This client paces sends with a ticker so the observed
// throughput reflects the server's ceiling rather than the client's greed.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	host := flag.String("host", "127.0.0.1:28000", "server address host:port")
	conns := flag.Int("conns", 1, "number of concurrent connections")
	rate := flag.Int("rate", 2000, "target messages per second across all connections")
	dur := flag.Duration("duration", 8*time.Second, "test duration")
	size := flag.Int("size", 256, "payload size in bytes")
	flag.Parse()

	var sent, recv, errs atomic.Int64

	// The server's default framer is LengthPrefixFramer, so every write needs a
	// 4-byte big-endian length header. Writing bare payload bytes does not
	// "work but counts less": the server reads the first four payload bytes as a
	// length, which for a zeroed buffer means a zero-length frame, so the run
	// reports healthy numbers while measuring nothing. Encode once and reuse.
	frame := make([]byte, 4+*size)
	binary.BigEndian.PutUint32(frame[:4], uint32(*size))

	perConnRate := *rate / *conns
	if perConnRate < 1 {
		perConnRate = 1
	}
	// Batch the ticker: a ticker whose period is a few microseconds cannot
	// keep up and silently caps the achieved rate. Send `batch`
	// messages per tick so that rate/conns/batch stays above the ticker's
	// practical floor (~1ms).
	batch := perConnRate / 20
	if batch < 1 {
		batch = 1
	}
	effective := perConnRate / batch
	if effective < 1 {
		effective = 1
	}
	interval := time.Second / time.Duration(effective)
	deadline := time.Now().Add(*dur)

	// Echo replies are counted on a shared atomic; the read loop must outlive
	// the send loop, so it is joined separately instead of racing it.
	var readers sync.WaitGroup
	var writers sync.WaitGroup
	// readStop bounds the reader loops: a ReadDeadline timeout is their normal
	// wake-up, so without a deadline-based exit condition they never finish.
	readStop := time.Now().Add(*dur + 500*time.Millisecond)

	for i := 0; i < *conns; i++ {
		conn, err := net.Dial("tcp", *host)
		if err != nil {
			errs.Add(1)
			continue
		}
		readers.Add(1)
		go func(c net.Conn) {
			defer readers.Done()
			defer c.Close()
			buf := make([]byte, 64*1024)
			// Read in whole frames: a reply carries its own length header, so
			// counting raw Read() bytes would inflate the message count by
			// however much the stream happened to coalesce.
			var hdr [4]byte
			for time.Now().Before(readStop) {
				_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
				if _, err := readFull(c, hdr[:]); err != nil {
					// A timeout is the normal wake-up: the reader polls until
					// readStop passes. Any other error ends the loop.
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						continue
					}
					return
				}
				n := binary.BigEndian.Uint32(hdr[:])
				if n > uint32(len(buf)) {
					return
				}
				if _, err := readFull(c, buf[:n]); err != nil {
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						continue
					}
					return
				}
				recv.Add(1)
			}
		}(conn)

		writers.Add(1)
		go func(c net.Conn) {
			defer writers.Done()
			tk := time.NewTicker(interval)
			defer tk.Stop()
			for range tk.C {
				if time.Now().After(deadline) {
					return
				}
				for range batch {
					if _, err := c.Write(frame); err != nil {
						errs.Add(1)
						return
					}
					sent.Add(1)
				}
			}
		}(conn)
	}

	writers.Wait()
	// Give in-flight echoes a moment to arrive before tearing readers down.
	time.Sleep(300 * time.Millisecond)
	readers.Wait()

	fmt.Printf("sent=%-10d recv=%-10d errs=%-4d target=%d/s send_rate=%.0f/s\n",
		sent.Load(), recv.Load(), errs.Load(), *rate,
		float64(sent.Load())/dur.Seconds())
}

// readFull reads exactly len(buf) bytes, so a frame split across TCP segments
// is reassembled rather than mis-counted.
func readFull(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
