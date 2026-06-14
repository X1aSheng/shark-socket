package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/X1aSheng/shark-socket/internal/app"
)

func main() {
	configPath := flag.String("config", os.Getenv("SHARK_CONFIG"), "path to JSON configuration file")
	flag.Parse()

	cfg, err := app.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	runtimeApp, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	if err := runtimeApp.Start(ctx); err != nil {
		stop()
		log.Fatalf("shark-socket start error: %v", err)
	}
	defer stop()
	log.Printf("shark-socket protocols=%v health=%s metrics=%s", runtimeApp.Protocols, cfg.HealthAddr, cfg.MetricsAddr)

	<-ctx.Done()

	shutdownTimeout, err := cfg.ShutdownDuration()
	if err != nil {
		log.Printf("shark-socket invalid shutdown duration: %v", err)
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := runtimeApp.Stop(shutdownCtx); err != nil {
		log.Printf("shark-socket stop error: %v", err)
	}
}
