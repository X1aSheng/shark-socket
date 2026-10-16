//go:build ignore

// shark-socket WorkerPool load server.
//
// This is the server half of a two-process load test. The client half is
// scripts/workerpool_baseline.go, run separately (ideally on another host).
//
// Splitting the two matters. In a single-process test the client competes with
// the server for CPU and fills its own socket buffers, so the measured
// throughput describes the client rather than the pool -- which is exactly what
// happened in the first round of local measurements.
//
// The server reports its own queue-full count, so drop rate is observed at the
// source instead of inferred from client-side loss.
//
// Usage:
//
//	go run scripts/workerpool_server.go -addr :18000 -workers 4 -queue 512 -metrics :18001
//
// Add -elastic -elastic-min 2 -elastic-max 8 to run the same load against the
// elastic pool. Everything else is held constant so the two series differ only
// in how the worker count is chosen.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
	sharkruntime "github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
)

var (
	flagAddr     = flag.String("addr", "0.0.0.0:18000", "listen address")
	flagMetrics  = flag.String("metrics", "", "if set, serve Prometheus metrics on this address")
	flagWorkers  = flag.Int("workers", 4, "worker goroutine count")
	flagQueue    = flag.Int("queue", 512, "task queue size")
	flagPolicy   = flag.String("policy", "drop", "queue-full policy: drop, block, close")
	flagDelay    = flag.Duration("handler-delay", 0, "artificial per-message handler latency")
	flagConns    = flag.Int64("max-conns", 0, "max concurrent connections (0 = unlimited)")
	flagEcho     = flag.Bool("echo", true, "echo the payload back to the client")
	flagReadyFn  = flag.String("ready-file", "", "if set, write the listen address here once accepting")
	flagStopFile = flag.String("stop-file", "", "if set, shut down when this path appears")
	// Elastic-pool flags. -elastic is what makes this server comparable against
	// the fixed-pool runs: without it the pool is the WorkerPool every earlier
	// measurement used, so the two series differ only in the pool.
	flagElastic    = flag.Bool("elastic", false, "use the elastic worker pool instead of the fixed one")
	flagElasticMin = flag.Int("elastic-min", 2, "elastic pool floor (and start size)")
	flagElasticMax = flag.Int("elastic-max", 8, "elastic pool ceiling")
	flagScaleUp    = flag.Float64("scale-up", 0, "queue occupancy that adds a worker (0 = package default)")
	flagScaleDown  = flag.Float64("scale-down", 0, "queue occupancy below which a worker may retire (0 = package default)")
)

func main() {
	flag.Parse()

	policy := shared.PolicyDrop
	switch *flagPolicy {
	case "block":
		policy = shared.PolicyBlock
	case "close":
		policy = shared.PolicyClose
	}

	metrics := observability.NewPrometheusMetrics()

	delay := *flagDelay
	echo := *flagEcho
	opts := []tcp.Option{
		tcp.WithAddr(*flagAddr),
		tcp.WithFramer(tcp.LengthPrefixFramer{MaxFrameBytes: 1024 * 1024}),
		tcp.WithHandler(func(sess core.Session, msg core.Message) error {
			if delay > 0 {
				time.Sleep(delay)
			}
			if echo {
				return sess.Send(msg.Payload)
			}
			return nil
		}),
		tcp.WithWorkers(*flagWorkers, *flagQueue, policy),
		tcp.WithMaxConnections(*flagConns),
	}
	if *flagElastic {
		opts = append(opts, tcp.WithElasticPool(*flagElasticMin, *flagElasticMax))
		if *flagScaleUp > 0 || *flagScaleDown > 0 {
			opts = append(opts, tcp.WithScaleRatio(*flagScaleUp, *flagScaleDown))
		}
	}
	srv := tcp.NewServer(opts...)

	if *flagMetrics != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			_, _ = w.Write([]byte(metrics.ExportText()))
		})
		go func() {
			srvMux := &http.Server{Addr: *flagMetrics, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
			if err := srvMux.ListenAndServe(); err != nil {
				fmt.Fprintf(os.Stderr, "metrics server: %v\n", err)
			}
		}()
	}

	gw := sharkruntime.NewGateway(sharkruntime.WithMetrics(metrics))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := gw.Register(srv); err != nil {
		fmt.Fprintf(os.Stderr, "register: %v\n", err)
		os.Exit(1)
	}
	if err := gw.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}

	actual := srv.Addr().String()
	fmt.Printf("READY %s\n", actual)
	os.Stdout.Sync()

	if *flagReadyFn != "" {
		_ = os.WriteFile(*flagReadyFn, []byte(actual), 0o644)
	}

	// Report process-level resource use on a ticker so the client side can tell
	// a saturated server from a saturated client.
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				var ms goruntime.MemStats
				goruntime.ReadMemStats(&ms)
				// worker count is sampled alongside memory: with -elastic it is the
				// only externally visible sign that scaling acted on the backlog.
				minW, maxW := srv.WorkerBounds()
				fmt.Printf("STATS goroutines=%d workers=%d bounds=(%d,%d) heapAllocMiB=%.1f numGC=%d\n",
					goruntime.NumGoroutine(), srv.WorkerCount(), minW, maxW,
					float64(ms.HeapAlloc)/(1024*1024),
					ms.NumGC)
			}
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	if *flagStopFile != "" {
		go func() {
			for {
				if _, err := os.Stat(*flagStopFile); err == nil {
					fmt.Println("stop file appeared, shutting down")
					sig <- syscall.SIGTERM
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}()
	}

	<-sig
	close(stop)

	fmt.Println("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutdownCancel()
	if err := gw.Stop(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "stop: %v\n", err)
	}

	// Final metrics so the client can scrape the authoritative drop count.
	fmt.Println("=== FINAL METRICS ===")
	for _, line := range strings.Split(metrics.ExportText(), "\n") {
		if strings.Contains(line, "tcp_task_queue_full_total") ||
			strings.Contains(line, "sessions_accepted_total") ||
			strings.Contains(line, "sessions_closed_total") {
			fmt.Println(line)
		}
	}
	fmt.Printf("=== END (peak goroutines seen above; GOMAXPROCS=%d NumCPU=%d) ===\n",
		goruntime.GOMAXPROCS(0), runtime.NumCPU())
}
