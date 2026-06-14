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
	defer stop()

	if err := runtimeApp.Start(ctx); err != nil {
		log.Printf("shark-socket start error: %v", err)
		stop()
		os.Exit(1)
	}
	log.Printf("shark-socket protocols=%v health=%s metrics=%s", runtimeApp.Protocols, cfg.HealthAddr, cfg.MetricsAddr)

	<-ctx.Done()
	timeout, err := cfg.ShutdownDuration()
	if err != nil {
		log.Fatal(err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := runtimeApp.Stop(shutdownCtx); err != nil {
		log.Fatal(err)
	}
}
