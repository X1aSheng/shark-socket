package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	logger := runtimeApp.Logger
	logger.Info("shark-socket started",
		"protocols", runtimeApp.Protocols,
		"health", cfg.HealthAddr,
		"metrics", cfg.MetricsAddr,
		"log_level", cfg.LogLevel,
	)

	// SIGHUP reloads the hot-reloadable subset (log level, blacklist, rate
	// limits). It is a separate channel from the context so that a rejected
	// reload is reported and survived rather than mistaken for shutdown
	// request: the process keeps serving with the previous configuration.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for {
			select {
			case <-hup:
				reload(runtimeApp, *configPath)
			case <-ctx.Done():
				return
			}
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), runtimeApp.ShutdownDuration())
	defer cancel()
	if err := runtimeApp.Stop(shutdownCtx); err != nil {
		logger.Error("shark-socket stop error", "error", err)
	}
}

func reload(runtimeApp *app.App, configPath string) {
	if err := runtimeApp.ReloadFile(configPath); err != nil {
		runtimeApp.Logger.Error("config reload rejected, keeping the running configuration", "error", err)
		return
	}
	current := runtimeApp.CurrentConfig()
	runtimeApp.Logger.Info("config reloaded",
		"log_level", current.LogLevel,
		"blacklist_entries", len(current.Blacklist),
		"rate_limit", rateLimitSummary(current),
	)
}

func rateLimitSummary(cfg app.Config) string {
	if cfg.RateLimit == nil {
		return "off"
	}
	window, err := cfg.RateLimit.WindowDuration()
	if err != nil {
		window = time.Second
	}
	return fmt.Sprintf("%d msg/%s", cfg.RateLimit.PerSecond, window)
}
