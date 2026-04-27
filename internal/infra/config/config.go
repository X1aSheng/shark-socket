package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// Reloader detects configuration changes and triggers reload.
type Reloader interface {
	// Start starts watching for config changes.
	Start() error
	// Stop stops watching for config changes.
	Stop() error
	// Reload triggers an immediate reload.
	Reload() error
	// Config returns the current configuration.
	Config() any
}

// ConfigFunc loads configuration from a source.
type ConfigFunc func() (any, error)

// FileReloader watches a config file and reloads it on changes.
type FileReloader struct {
	path      string
	interval  time.Duration
	parser    ConfigFunc
	config    any
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	onReload  func(any)
	lastMod   int64
}

// Option configures the FileReloader.
type Option func(*FileReloader)

// WithReloadInterval sets the check interval for file changes.
func WithReloadInterval(d time.Duration) Option {
	return func(r *FileReloader) { r.interval = d }
}

// WithOnReload sets the callback when config is reloaded.
func WithOnReload(fn func(any)) Option {
	return func(r *FileReloader) { r.onReload = fn }
}

// NewFileReloader creates a FileReloader from a JSON file.
// target must be a pointer to the configuration struct.
func NewFileReloader(path string, target any, opts ...Option) *FileReloader {
	r := &FileReloader{
		path:     path,
		interval: 5 * time.Second,
		parser:   func() (any, error) { return loadJSONFile(path, target) },
		config:   target,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func loadJSONFile(path string, target any) (any, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrConfigNotFound
		}
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Handle both pointer and non-pointer targets
	dst := target
	if err := json.Unmarshal(data, dst); err != nil {
		return nil, err
	}
	return dst, nil
}

// Start starts watching for config changes.
func (r *FileReloader) Start() error {
	r.ctx, r.cancel = context.WithCancel(context.Background())

	// Initial load
	if err := r.Reload(); err != nil {
		return err
	}

	go r.watchLoop()
	return nil
}

// Stop stops watching for config changes.
func (r *FileReloader) Stop() error {
	if r.cancel != nil {
		r.cancel()
	}
	return nil
}

// Reload triggers an immediate reload.
func (r *FileReloader) Reload() error {
	info, err := os.Stat(r.path)
	if err != nil {
		return err
	}

	newMod := info.ModTime().UnixNano()
	if newMod == r.lastMod {
		return nil
	}
	r.lastMod = newMod

	newConfig, err := r.parser()
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.config = newConfig
	r.mu.Unlock()

	if r.onReload != nil {
		r.onReload(newConfig)
	}
	return nil
}

// Config returns the current configuration.
func (r *FileReloader) Config() any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config
}

func (r *FileReloader) watchLoop() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			_ = r.Reload()
		}
	}
}

// ErrConfigNotFound is returned when config file doesn't exist.
var ErrConfigNotFound = errors.New("config file not found")

// GatewayConfig represents gateway configuration that can be reloaded.
type GatewayConfig struct {
	ShutdownTimeout string `json:"shutdown_timeout"`
	MetricsAddr     string `json:"metrics_addr"`
	EnableMetrics   bool   `json:"enable_metrics"`
	LogLevel       string `json:"log_level"`
}