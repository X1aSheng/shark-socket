//go:build ignore

// shark-socket WorkerPool saturation baseline.
//
// Purpose: measure the drop rate of the single shared WorkerPool under burst
// message load, so the "does the single-pool design survive a burst" question
// has data instead of speculation.
//
// The server under test is configured through the same options an operator
// would use (WithWorkers / TaskQueueSize / FullPolicy). Drops are counted from
// the transport's own metric, tcp_task_queue_full_total (server.go:213), so we
// observe the exact event the framework reports rather than inferring it.
//
// Usage:
//
//	go run scripts/workerpool_baseline.go -workers 4 -queue 512 -conns 200 -duration 20s
//	go run scripts/workerpool_baseline.go -workers 4 -queue 512 -conns 200 -handler-delay 5ms
//	go run scripts/workerpool_baseline.go -sweep -conns 200
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
	sharkruntime "github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
)

var (
	flagWorkers      = flag.Int("workers", 4, "worker goroutine count")
	flagQueue        = flag.Int("queue", 512, "task queue size")
	flagConns        = flag.Int("conns", 200, "concurrent client connections")
	flagDuration     = flag.Duration("duration", 20*time.Second, "test duration")
	flagSize         = flag.Int("size", 256, "payload size in bytes")
	flagHandlerDelay = flag.Duration("handler-delay", 0, "artificial per-message handler latency (slow-handler scenario)")
	flagPolicy       = flag.String("policy", "drop", "queue-full policy: drop, block, close")
	flagSweep        = flag.Bool("sweep", false, "sweep conns across a range instead of a single run")
	flagTimeout      = flag.Duration("timeout", 10*time.Second, "per-read timeout on the client")
	flagPipeline     = flag.Bool("pipeline", true, "pipeline mode: flood without waiting for replies (required to saturate the queue)")
	flagHost         = flag.String("host", "", "target server address (empty = start a local server in-process)")
	flagMetricsHost  = flag.String("metrics-host", "127.0.0.1:18001", "metrics endpoint host:port (remote mode only)")
)

// counters is the client-observed truth: what we sent versus what came back.
type counters struct {
	sent     atomic.Int64
	received atomic.Int64
	sendErr  atomic.Int64
	readErr  atomic.Int64
}

// promDropCounter counts occurrences of a metric name in exported text.
// PrometheusMetrics has no programmatic getter, so we parse ExportText.
// This is a baseline tool; the scrape happens once, after the run.
func promDropCounter(text, name string) float64 {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, name) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil {
			return v
		}
	}
	return 0
}

func main() {
	flag.Parse()

	fmt.Println("shark-socket WorkerPool saturation baseline")
	fmt.Printf("go=%s os=%s arch=%s NumCPU=%d GOMAXPROCS=%d\n\n",
		goruntime.Version(), goruntime.GOOS, goruntime.GOARCH,
		goruntime.NumCPU(), goruntime.GOMAXPROCS(0))

	if *flagSweep {
		runSweep()
		return
	}
	res := run(*flagConns)
	printResult(res)
}

// result captures one configuration's outcome.
type result struct {
	workers      int
	queue        int
	policy       string
	conns        int
	handlerDelay time.Duration

	sent      int64
	received  int64
	sendErr   int64
	readErr   int64
	queueFull float64 // server-side tcp_task_queue_full_total

	elapsed time.Duration
	heapMB  float64
}

func (r result) dropRate() float64 {
	// The framework reports a queue-full event per rejected Submit. Compare it
	// against what the client actually managed to send: those are the messages
	// that could have been dropped. Using sent (not received) as the denominator
	// avoids counting messages lost elsewhere (socket buffer, client read) as
	// pool drops.
	if r.sent == 0 {
		return 0
	}
	return r.queueFull / float64(r.sent) * 100
}

func (r result) lossRate() float64 {
	// End-to-end: sent versus echoed back. This is what a user actually sees.
	if r.sent == 0 {
		return 0
	}
	return float64(r.sent-r.received) / float64(r.sent) * 100
}

func (r result) msgPerSec() float64 {
	if r.elapsed <= 0 {
		return 0
	}
	return float64(r.received) / r.elapsed.Seconds()
}

func runSweep() {
	levels := []int{1, 10, 50, 100, 200, 500, 1000}
	fmt.Printf("%-8s %-12s %-10s %-10s %-12s %-12s\n",
		"conns", "sent", "recv", "drop%", "loss%", "msg/s")
	fmt.Println(strings.Repeat("-", 72))

	var rows []result
	for _, c := range levels {
		// The queue-full counter is cumulative on the server, so without a
		// reset each row would carry every previous row's drops and drop% would
		// exceed 100%. Read it as a delta instead.
		base := scrapeQueueFull()
		r := run(c)
		if *flagHost != "" && r.queueFull >= 0 {
			delta := r.queueFull - base
			if delta < 0 {
				// The server restarted between the two scrapes; treat the
				// current reading as the delta rather than reporting nonsense.
				delta = r.queueFull
			}
			r.queueFull = delta
		}
		rows = append(rows, r)
		fmt.Printf("%-8d %-12d %-10d %-10.3f %-12.3f %-12.0f\n",
			r.conns, r.sent, r.received, r.dropRate(), r.lossRate(), r.msgPerSec())
	}

	fmt.Println()
	first, last := rows[0], rows[len(rows)-1]
	fmt.Printf("drop rate %d->%d conns: %.3f%% -> %.3f%%\n",
		first.conns, last.conns, first.dropRate(), last.dropRate())
	fmt.Printf("loss rate %d->%d conns: %.3f%% -> %.3f%%\n",
		first.conns, last.conns, first.lossRate(), last.lossRate())
}

// scrapeQueueFull reads the current cumulative counter so a sweep can compute
// per-configuration deltas.
func scrapeQueueFull() float64 {
	if *flagHost == "" {
		return 0
	}
	return scrapeRemote("http://" + *flagMetricsHost + "/metrics")
}

func run(conns int) result {
	policy := shared.PolicyDrop
	switch *flagPolicy {
	case "block":
		policy = shared.PolicyBlock
	case "close":
		policy = shared.PolicyClose
	}

	// Remote mode: drive an already-running server (typically on another host)
	// and scrape its queue-full counter over HTTP. Local mode: start a server
	// in-process and read its metrics directly.
	remote := *flagHost != ""
	var (
		metrics    *observability.PrometheusMetrics
		metricsURL string
		stopServer func()
	)

	if remote {
		metricsURL = "http://" + *flagMetricsHost + "/metrics"
	} else {
		metrics = observability.NewPrometheusMetrics()
	}

	if !remote {
		handlerDelay := *flagHandlerDelay
		srv := tcp.NewServer(
			tcp.WithAddr("127.0.0.1:0"),
			tcp.WithFramer(tcp.LengthPrefixFramer{MaxFrameBytes: 1024 * 1024}),
			tcp.WithHandler(func(sess core.Session, msg core.Message) error {
				if handlerDelay > 0 {
					time.Sleep(handlerDelay)
				}
				return sess.Send(msg.Payload)
			}),
			tcp.WithWorkers(*flagWorkers, *flagQueue, policy),
		)

		gw := sharkruntime.NewGateway(sharkruntime.WithMetrics(metrics))
		if err := gw.Register(srv); err != nil {
			panic(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if err := gw.Start(ctx); err != nil {
			panic(err)
		}
		addr := srv.Addr().String()
		stopServer = func() {
			cancel()
			_ = gw.Stop(context.Background())
		}
		remoteAddr = addr
	}

	addr := remoteAddr
	if addr == "" {
		addr = *flagHost
	}

	payload := make([]byte, *flagSize)
	for i := range payload {
		payload[i] = byte(i)
	}

	var c counters
	var wg sync.WaitGroup
	end := time.Now().Add(*flagDuration)

	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runClient(addr, payload, end, &c)
		}()
	}

	wg.Wait()

	// Give in-flight writes a moment to flush before scraping, so the drop
	// counter includes everything the run produced.
	time.Sleep(300 * time.Millisecond)

	var queueFull float64
	if remote {
		queueFull = scrapeRemote(metricsURL)
	} else {
		queueFull = promDropCounter(metrics.ExportText(), "tcp_task_queue_full_total")
	}
	if stopServer != nil {
		stopServer()
	}

	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)

	return result{
		workers:      *flagWorkers,
		queue:        *flagQueue,
		policy:       *flagPolicy,
		conns:        conns,
		handlerDelay: *flagHandlerDelay,
		sent:         c.sent.Load(),
		received:     c.received.Load(),
		sendErr:      c.sendErr.Load(),
		readErr:      c.readErr.Load(),
		queueFull:    queueFull,
		elapsed:      *flagDuration,
		heapMB:       float64(ms.HeapAlloc) / (1024 * 1024),
	}
}

var remoteAddr string

// scrapeRemote reads the authoritative queue-full counter from a server running
// on another host, so the drop rate is measured at the source rather than
// inferred from client-side loss.
func scrapeRemote(url string) float64 {
	// The metrics endpoint is reached through an SSH tunnel on loopback. Many
	// environments route loopback through an HTTP proxy, which answers with an
	// error page (or nothing) instead of the metrics text; bypassing the proxy
	// for loopback targets is required for the scrape to work at all.
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scrape %s: %v\n", url, err)
		return -1
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1
	}
	return promDropCounter(string(body), "tcp_task_queue_full_total")
}

func runClient(addr string, payload []byte, end time.Time, c *counters) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		c.sendErr.Add(1)
		return
	}
	defer func() { _ = conn.Close() }()

	if *flagTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(*flagTimeout))
	}
	if *flagHandlerDelay > 0 {
		// A slow handler means replies arrive later than the client timeout
		// would otherwise allow; scale the deadline so a healthy run is not
		// misreported as loss.
		_ = conn.SetReadDeadline(time.Now().Add(*flagTimeout + *flagHandlerDelay*4))
	}

	framer := tcp.LengthPrefixFramer{MaxFrameBytes: 1024 * 1024}
	reply := make([]byte, 64*1024)

	if *flagPipeline {
		// Pipeline mode: write as fast as the socket accepts and never block on
		// the reply. This is what actually saturates the worker queue. Echo mode
		// self-throttles to the server's throughput, so the queue can never fill
		// and the drop rate is structurally always zero -- a measurement that
		// looks healthy but proves nothing.
		go func() {
			for time.Now().Before(end) {
				if _, err := readFrame(conn, framer, reply); err != nil {
					return
				}
				c.received.Add(1)
			}
		}()
		for time.Now().Before(end) {
			if err := framer.WriteFrame(conn, payload); err != nil {
				c.sendErr.Add(1)
				return
			}
			c.sent.Add(1)
		}
		return
	}

	for time.Now().Before(end) {
		if err := framer.WriteFrame(conn, payload); err != nil {
			c.sendErr.Add(1)
			return
		}
		c.sent.Add(1)

		// Echo mode: the server replies with the same payload, so the client
		// blocks on the read. That naturally paces the loop to the server's
		// throughput, which is exactly what we want to measure.
		n, err := readFrame(conn, framer, reply)
		if err != nil {
			c.readErr.Add(1)
			return
		}
		_ = n
		c.received.Add(1)
	}
}

// readFrame reads one length-prefixed frame using the transport framer via an
// io.Reader adapter, so the client path exercises the same framing code.
func readFrame(conn net.Conn, framer tcp.LengthPrefixFramer, buf []byte) (int, error) {
	frame, err := framer.ReadFrame(conn)
	if err != nil {
		return 0, err
	}
	return copy(buf, frame), nil
}

func printResult(r result) {
	fmt.Println("configuration")
	fmt.Printf("  workers        %d\n", r.workers)
	fmt.Printf("  queue          %d\n", r.queue)
	fmt.Printf("  policy         %s\n", r.policy)
	fmt.Printf("  conns          %d\n", r.conns)
	if r.handlerDelay > 0 {
		fmt.Printf("  handler delay  %s\n", r.handlerDelay)
	}
	fmt.Println()
	fmt.Println("client observed")
	fmt.Printf("  sent           %d\n", r.sent)
	fmt.Printf("  received       %d\n", r.received)
	fmt.Printf("  send errors    %d\n", r.sendErr)
	fmt.Printf("  read errors    %d\n", r.readErr)
	fmt.Println()
	fmt.Println("server observed")
	fmt.Printf("  queue-full     %.0f  (tcp_task_queue_full_total)\n", r.queueFull)
	fmt.Println()
	fmt.Println("derived")
	fmt.Printf("  drop rate      %.3f%%   (queue-full / sent)\n", r.dropRate())
	fmt.Printf("  loss rate      %.3f%%   (sent - received) / sent\n", r.lossRate())
	fmt.Printf("  throughput     %.0f msg/s\n", r.msgPerSec())
	fmt.Printf("  heap in use    %.1f MiB\n", r.heapMB)
}

var _ = binary.BigEndian
var _ = sort.Ints
