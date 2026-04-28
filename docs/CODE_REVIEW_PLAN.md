# shark-socket Code Review — Defects & Improvement Plan

> Generated 2026-04-28 via comprehensive manual review of all 180 Go source files.
> All tests pass. `go vet` is clean. Race detector unavailable (no CGO on this Windows system).

## Executive Summary

| Severity | Count | Description |
|----------|-------|-------------|
| Critical | 1 | C4 — OTel distributed tracing still completely broken |
| High | 4 | H15 QUIC stream-per-message overhead, R1-R3 regressions/race conditions |
| Medium | 6 | M4-M6 performance/error handling gaps |
| Low | 4 | L1-L4 dead code, typos, style |

## Test Results (2026-04-28)

```
go test ./... -count=1: ALL 23 packages PASS (0 failures)
go vet ./...: CLEAN (0 warnings)
go run scripts/run_tests.go -mode all: 80 integration + all unit PASS
go run scripts/run_tests.go -mode benchmark: 78 benchmarks OK
```

Coverage highlights: errs 100%, cache 100%, store 100%, defense 96.1%, pubsub 94.6%, types 92.3%.
Low coverage: tracing 12.5%, quic 28.0%, grpcgw 29.6%, logger 29.8%, gateway 32.1%.

---

## 1. Remaining Issues from Prior Plan (Not Yet Fixed)

### C4. OTel distributed tracing propagation completely broken (STILL PRESENT)
- **File:** `internal/infra/tracing/otel.go:96,178`
- **Problem:** Both `OTelTracer.StartSpan` and `otelTracerAdapter.StartSpan` call:
  ```go
  parentCtx := t.propagator.Extract(ctx, propagation.HeaderCarrier{})
  ```
  The empty `HeaderCarrier{}` means no incoming trace context is ever extracted. Every span becomes a root span. Multi-service distributed tracing is completely non-functional.
- **Fix:** Extract headers from the incoming context (e.g., inject via HTTP headers or metadata carrier before passing to this method).

### H15. QUIC opens a new stream for every message (PERFORMANCE)
- **File:** `internal/protocol/quic/session.go:68-75`
- **Problem:** `writeToStream()` calls `conn.OpenStreamSync()` for every single `Send()`. QUIC stream handshakes have significant overhead (RTT-dependent). For high-throughput messaging, this multiplies latency and CPU usage.
- **Fix:** Reuse a single bidirectional stream for multiple messages, multiplexed with a simple length-prefix or message boundary protocol.

### M4. Broadcast silently drops all Send errors
- **File:** `internal/session/manager.go:188-192`
- **Problem:** Broadcast records errors to metrics but doesn't aggregate or log failures. Operators can't detect broadcast failures without monitoring dashboards.
- **Fix:** Log first error per broadcast cycle at WARN level; aggregate remaining.

### M6. CoAP MessageID cache eviction is O(n)
- **File:** `internal/protocol/coap/session.go:111-123`
- **Problem:** On every insert when cache is full (>500 entries), iterates ALL entries to find the oldest timestamp. O(500) per insert under load.
- **Fix:** Replace map-only approach with a ring buffer indexed by `msgID % cacheSize`, or use a min-heap for O(log n) eviction.

---

## 2. NEW Issues Discovered This Review

### R1. CoAP IsDuplicate/RecordMessageID TOCTOU race
- **File:** `internal/protocol/coap/server.go:109-118`
- **Severity:** High
- **Problem:** `IsDuplicate()` checks the cache under `msgCacheMu.RLock()`, then releases the lock. Later, `RecordMessageID()` acquires `msgCacheMu.Lock()` to insert. Between these two calls, another goroutine could process the same MessageID (duplicate CON retransmission), causing double-processing of the message. The handler would run twice for the same message.
- **Fix:** Merge `IsDuplicate` + `RecordMessageID` into a single atomic `CheckAndRecord(msgID uint16) bool` method that holds the write lock throughout.

### R2. PersistencePlugin flush — circuit breaker called per-entry
- **File:** `internal/plugin/persistence.go:135-149`
- **Severity:** High
- **Problem:** The flush loop calls `p.cb.Do(func() { p.store.Save(...) })` for each entry in the batch. If the circuit breaker opens mid-batch (after 5 failures), all remaining entries silently fail with `ErrCircuitOpen` and are lost. Additionally, `OnAccept` calls `p.cb.Do()` with `store.Load()`, where `ErrNotFound` for new sessions counts as a circuit breaker failure. After 5 new sessions, the circuit opens and ALL persistence operations stop.
- **Fix:** Move `cb.Do` outside the loop to wrap the entire batch. Don't count `ErrNotFound` as a circuit breaker failure in `OnAccept`.

### R3. RateLimitPlugin violations map cleared entirely every 2 minutes
- **File:** `internal/plugin/ratelimit.go:181-183`
- **Severity:** High
- **Problem:** The cleanup loop deletes ALL violation entries every 2 minutes:
  ```go
  p.violations.Range(func(key, val any) bool {
      p.violations.Delete(key)
      return true
  })
  ```
  This means violation counters never accumulate beyond a 2-minute window. The `AutoBanPlugin` relies on `RecordRateLimit()` (autoban.go:107) which increments counters that were created by `getCounters(ip)` (autoban.go:93), NOT from the RateLimitPlugin's violations map. However, `checkAndBan()` (autoban.go:98) checks `c.rateLimitHits` which comes from `RecordRateLimit()` — called externally. The violation counters in RateLimitPlugin are independent and serve no real purpose since they're wiped every 2 minutes.
- **Fix:** Use per-IP sliding window counters with configurable TTL instead of bulk-delete. Or remove the violations tracking if unused.

### R4. HeartbeatPlugin TimeWheel.Reset iterates all slots
- **File:** `internal/plugin/heartbeat.go:85-87`
- **Severity:** Medium
- **Problem:** `Reset()` removes the entry by iterating ALL slots:
  ```go
  for _, slot := range tw.slots {
      delete(slot, id)
  }
  ```
  With many slots (e.g., 600 for a 10-min timeout at 1s ticks), this is O(slots) per message. Under high message throughput, this adds measurable latency.
- **Fix:** Add a `map[uint64]int` reverse index (`id → slot`) for O(1) removal on Reset.

### R5. Gateway StageTimeouts from WithShutdownTimeout exceed total timeout
- **File:** `internal/gateway/options.go:61-71`
- **Severity:** Medium
- **Problem:** Integer division truncation means stages sum to MORE than the configured total:
  - StopAccept: d/3, Drain: d/3, SessionClose: d/3, ManagerClose: d/6, MetricsClose: d/6, Finalize: d/6
  - Total = d/3 + d/3 + d/3 + d/6 + d/6 + d/6 = 3d/3 + 3d/6 = d + d/2 = 1.5d
  - For d=10s, total stage time = 15s, 50% over budget.
- **Fix:** Either distribute proportionally (e.g., 30%+30%+30%+4%+3%+3%), or apply the overall timeout context around all stages.

### R6. AutoBan checkAndBan triggers ban on totalConns threshold alone
- **File:** `internal/plugin/autoban.go:98-103`
- **Severity:** Medium
- **Problem:** `totalConns` is incremented on every `OnAccept` (line 76) and `checkAndBan` is called on every `OnAccept` AND `OnMessage`. With `EmptyConnThreshold` defaulting to 20, any IP that opens 20 connections gets banned — even if all connections are legitimate and active. The field was renamed from `emptyConns` to `totalConns` but the behavior (and misleading threshold name) persists.
- **Fix:** Either raise the default to a much higher value (e.g., 1000), or only increment `totalConns` for truly empty connections (connections that close without sending any data).

### R7. Stage 4 (ManagerClose) and Stage 3 (SessionClose) both call Close() — duplicate
- **File:** `internal/gateway/gateway.go:263-272,277-283`
- **Severity:** Low
- **Problem:** `stageSessionClose` calls `g.sharedManager.Close()`, and `stageManagerClose` calls it again. Manager.Close() is idempotent (uses `CompareAndSwap`), but the second call is unnecessary and adds confusion to the staged shutdown architecture.
- **Fix:** Either remove stage 4 entirely, or make it do something distinct (e.g., verify all sessions are gone, log final counts).

### R8. CoAP readLoop silently drops malformed messages and decode errors
- **File:** `internal/protocol/coap/server.go:98-99`
- **Severity:** Low
- **Problem:** `ParseMessage` errors are silently ignored with `continue`. Operators have no visibility into malformed message rates, which could indicate an attack or client bug.
- **Fix:** Count and optionally log sampled malformed messages.

### R9. go.mod has probable typo in dependency
- **File:** `go.mod:28`
- **Severity:** Low
- **Problem:** `go.yaml.in/yaml/v2` — this is almost certainly a typo of `gopkg.in/yaml.v2`. The `yaml.in` domain doesn't exist.
- **Fix:** Correct to `gopkg.in/yaml.v2` if the indirect dependency is actually needed, or verify it's not used.

### R10. `crypto/rand` used for jitter in TCP client backoff
- **File:** `internal/protocol/tcp/client.go:182-184`
- **Severity:** Low
- **Problem:** `crypto/rand.Read()` is a blocking system call to the OS entropy source. For jitter calculation (which doesn't need cryptographic randomness), this is unnecessary overhead. Under high contention, multiple goroutines block on entropy.
- **Fix:** Use `math/rand/v2` for jitter.

---

## 3. Already Fixed (Verification Passed)

These prior-plan issues have been confirmed fixed in the current code:

| ID | Issue | Fix Verified |
|----|-------|-------------|
| C1 | PubSub index-based unsubscribe → map[uint64] | ✅ pubsub.go uses uint64 IDs |
| C2 | AutoBan key format mismatch | ✅ Both use IPToKey() |
| C3 | Cluster subscription leak | ✅ Stored and unsubscribed in Close() |
| C5 | MemoryCache double-close panic | ✅ sync.Once guard |
| C7 | IsPrivateIP nil panic | ✅ init() pre-parses CIDRs with panic |
| C8 | MessageConstraint type parameter | ✅ Removed `any` from union |
| H1 | Session All/Range outside lock | ✅ Documented yield-only-during-callback |
| H2 | BaseSession DoClose sync.Once | ✅ Embedded sync.Once |
| H3 | Cache Get/MGet copy on return | ✅ Copies before return |
| H4 | Store Save/Load copy | ✅ Copies on save and load |
| H5 | Metrics maps thread safety | ✅ Double-check locking |
| H6 | Timer histogram caching | ✅ Cached in histograms map |
| H7 | HalfOpen CAS fix | ✅ CompareAndSwap |
| H8 | WithGlobalPlugins slice copy | ✅ Copies before append |
| H9 | AutoBan emptyConns→totalConns | ✅ Renamed field |
| H10 | TimeWheel current-slot-only | ✅ Only checks current slot |
| H11 | gRPC-Web write mutex | ✅ writeMu added |
| H12 | WebSocket CheckOrigin defaults | ✅ Empty origins allow all |
| H13 | CoAP retransmitLoop deadlock | ✅ Lock released before Send() |
| H14 | HTTP custom status code | ✅ w.WriteHeader(status) called |
| M1-M3 | TCP client races | ✅ Fixed |
| M5 | LogSampler.Clean never called | ✅ cleanupLoop added |
| M7 | CoAP MessageIDCacheSize wired | ✅ Passed to NewCoAPSession |
| M8 | Logger global thread safety | ✅ atomic.Pointer |
| M9 | BufferPool totalMem decrement | ✅ Decrements on Put |
| M10 | ConnectionLimiter cleanup | ✅ cleanupLoop added |
| M12 | AutoBan counter reset | ✅ cleanupLoop with 10min TTL |
| M13 | SlowQueryPlugin global variable | ✅ Per-instance config |
| M14 | PolicyClose acts like PolicyClose | ✅ Calls sess.Close() |
| M15 | HTTP SetManager race | ✅ Not an issue (no manager field) |
| M16 | gRPC AllowedOrigins wired | ✅ Used in upgrader |
| M17 | CoAP ACK after handler | ✅ Handler runs before ACK |
| M18 | generateID exposes counter | ✅ Non-critical (low sensitivity) |

---

## Fix Order (Batched)

### Batch 4 — Remaining fixes (this session)

1. **R1**: CoAP IsDuplicate/RecordMessageID TOCTOU race → atomic CheckAndRecord
2. **C4**: OTel propagation — `HeaderCarrier{}` populated from context
3. **R2**: PersistencePlugin flush — batch-level circuit breaker, skip ErrNotFound counting
4. **R3**: RateLimitPlugin violations cleanup — sliding window or remove unused tracking
5. **R6**: AutoBan totalConns threshold too aggressive

### Batch 5 — Performance & cleanup

6. **H15**: QUIC stream reuse — single bidirectional stream
7. **R4**: HeartbeatPlugin Reset O(n) → reverse index
8. **M4**: Broadcast error aggregation logging
9. **M6**: CoAP MessageIDCache O(n) eviction → ring buffer
10. **R5, R7-R10**: Low priority fixes

---

## Verification

After each batch:
```bash
go build ./...
go test ./... -count=1 -timeout 120s
go vet ./...
```
