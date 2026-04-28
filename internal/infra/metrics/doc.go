// Package metrics provides metrics collection interfaces and Prometheus integration.
//
// This package defines the Metrics interface and built-in implementations for
// observing framework behavior. All framework components emit metrics through
// this interface, enabling unified monitoring and alerting.
//
// # Metrics Interface
//
//	type Metrics interface {
//	    Counter(name string, labels ...string) CounterVec
//	    Gauge(name string, labels ...string) GaugeVec
//	    Histogram(name string, labels ...string) HistogramVec
//	    Timer(name string, labels ...string) TimerVec
//	}
//
//	type CounterVec interface {
//	    Inc()
//	    Add(n float64)
//	}
//
//	type GaugeVec interface {
//	    Set(v float64)
//	    Inc()
//	    Dec()
//	}
//
//	type HistogramVec interface {
//	    Observe(v float64)
//	}
//
//	type TimerVec interface {
//	    ObserveDuration(start time.Time)
//	}
//
// # Built-in Metrics
//
// The framework pre-registers comprehensive metrics under shark_* namespace:
//
// ## Connection Metrics
//
// | Metric | Type | Labels | Description |
// |--------|------|--------|-------------|
// | shark_connections_total | Counter | protocol | Total connections opened |
// | shark_connections_active | Gauge | protocol | Current active connections |
// | shark_connection_errors_total | Counter | protocol | Connection failures |
//
// ## Message Metrics
//
// | Metric | Type | Labels | Description |
// |--------|------|--------|-------------|
// | shark_messages_total | Counter | protocol, type | Total messages processed |
// | shark_message_bytes | Histogram | protocol, direction | Message sizes |
// | shark_message_duration_seconds | Histogram | protocol | Processing time |
//
// ## Plugin Metrics
//
// | Metric | Type | Labels | Description |
// |--------|------|--------|-------------|
// | shark_plugin_duration_seconds | Histogram | plugin | Plugin execution time |
// | shark_plugin_panics_total | Counter | plugin | Plugin panic count |
//
// ## Resource Metrics
//
// | Metric | Type | Labels | Description |
// |--------|------|--------|-------------|
// | shark_worker_queue_depth | Gauge | protocol | Worker queue size |
// | shark_worker_panics_total | Counter | protocol | Worker panic count |
// | shark_session_lru_evictions_total | Counter | - | LRU evictions |
// | shark_rejected_connections_total | Counter | reason | Rejected connections |
// | shark_write_queue_full_total | Counter | protocol | Write queue full events |
// | shark_fd_usage_ratio | Gauge | - | File descriptor usage |
// | shark_overloaded | Gauge | - | Overload status (0/1) |
//
// ## BufferPool Metrics
//
// | Metric | Type | Labels | Description |
// |--------|------|--------|-------------|
// | shark_bufferpool_hits_total | Counter | level | Pool cache hits |
// | shark_bufferpool_misses_total | Counter | level | Pool cache misses |
//
// Note: level = "level0" (Micro) through "level4" (Large)
//
// # Usage
//
// Simple counter:
//
//	metrics.DefaultMetrics().Counter("shark_requests_total", "GET").Inc()
//
// Timed histogram:
//
//	start := time.Now()
//	processRequest()
//	metrics.DefaultMetrics().Timer("shark_request_duration").ObserveDuration(start)
//
// # Prometheus Exposition
//
// Metrics are exposed in Prometheus text format at /metrics endpoint:
//
//	# HELP shark_connections_total Total connections opened
//	# TYPE shark_connections_total counter
//	shark_connections_total{protocol="tcp"} 12345
//
// Integration with Prometheus configuration:
//
//	scrape_configs:
//	  - job_name: 'shark-socket'
//	    static_configs:
//	      - targets: ['localhost:9090']
//
// # Custom Metrics
//
// Create custom metrics for application monitoring:
//
//	var (
//	    customCounter = metrics.DefaultMetrics().Counter(
//	        "myapp_requests_total",
//	        "method", "path",
//	    )
//	    customHistogram = metrics.DefaultMetrics().Histogram(
//	        "myapp_request_size_bytes",
//	        "direction",
//	    )
//	)
//
// # Go Lang/Go-Routine Metrics
//
// The framework can expose Go runtime metrics:
//
//	metrics.GoRuntimeStats()  // Memory, goroutine count, GC stats
//
// Useful for detecting:
//   - Goroutine leaks (increasing count over time)
//   - Memory pressure (heap growth)
//   - GC thrashing (frequent GC pauses)
//
// # Health Integration
//
// Metrics combine with health endpoints:
//
//	GET /healthz
//	Response: {"status":"healthy","uptime":"2h30m"...}
//
//	GET /readyz
//	Response: {"status":"ready"}
//
// Alert on metrics combinations:
//
//	# Alert: High connection count
//	expr: shark_connections_active > 90000
//	for: 5m
//
//	# Alert: Worker queue backing up
//	expr: shark_worker_queue_depth > 100
//	for: 1m
//
// # Performance Characteristics
//
// Metrics operations use atomic operations:
//
//	Counter.Add() → atomic.AddUint64()  // ~10ns
//	Gauge.Set()   → atomic.StoreInt64() // ~10ns
//	Histogram.Observe() → atomic stats // ~100ns
//
// Actual Prometheus export happens asynchronously in background.
// Metrics collection does NOT block application threads.
//
// # Grafana Dashboard
//
// Recommended panels:
//
//  1. Connection Overview
//     - Active connections by protocol
//     - Connection rate (connections/sec)
//     - Connection errors rate
//
//  2. Throughput
//     - Messages/sec by protocol
//     - Message bytes by direction
//     - Average message size
//
//  3. Latency
//     - P50/P95/P99 processing time
//     - Write queue wait time
//
//  4. Resources
//     - Worker queue depth
//     - BufferPool hit rate
//     - Memory usage
//
//  5. Health
//     - Overload status
//     - FD usage ratio
//     - Error rates
//
// # Thread Safety
//
// All operations are thread-safe:
//   - Atomic counters for hot paths
//   - Internal locks only for registration
//   - Async export prevents blocking
//
// # No-Op Implementation
//
// Disable metrics with no-op implementation:
//
//	metrics.SetDefault(metrics.NopMetrics())
//
// Useful for benchmarks or testing environments.
package metrics
