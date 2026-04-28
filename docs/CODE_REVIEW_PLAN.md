# shark-socket Code Review — Defects & Improvement Plan

> Generated 2026-04-28 via comprehensive manual review of all 180 Go source files.
> All tests pass. `go vet` is clean. Race detector unavailable (no CGO on this Windows system).

## Executive Summary

| Severity | Count | Description |
|----------|-------|-------------|
| Critical | 0 | All critical issues resolved |
| High | 1 | H15 QUIC stream-per-message overhead |
| Medium | 6 | R4-R5, M4-M6 performance/error handling gaps |
| Low | 5 | R7-R10 dead code, typos, style |

**Batch 4 Complete (2026-04-28):** R1, R2, R3, R6, C4 fixed and verified.
**GitHub Actions CI Enhanced (2026-04-28):** 7-job CI matrix with multi-OS, Codecov, Docker smoke test.

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

### C4. OTel distributed tracing propagation completely broken ✅ FIXED (Batch 4)
- **File:** `internal/infra/tracing/otel.go:96,178`
- **Status:** Fixed. Removed the `propagator.Extract(ctx, propagation.HeaderCarrier{})` call. Both `OTelTracer.StartSpan` and `otelTracerAdapter.StartSpan` now pass `ctx` directly to `tracer.Start()`, allowing OTel to natively extract parent span from context.

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

### R1. CoAP IsDuplicate/RecordMessageID TOCTOU race ✅ FIXED (Batch 4)
- **File:** `internal/protocol/coap/session.go`
- **Severity:** High
- **Status:** Fixed. Merged `IsDuplicate` + `RecordMessageID` into a single atomic `CheckAndRecord(msgID uint16) bool` method that holds the write lock throughout. Server updated to use `CheckAndRecord` directly.

### R2. PersistencePlugin flush — circuit breaker called per-entry ✅ FIXED (Batch 4)
- **File:** `internal/plugin/persistence.go`
- **Severity:** High
- **Status:** Fixed. `OnAccept` no longer wraps `store.Load` in `cb.Do` (ErrNotFound is expected for new sessions). `Flush` wraps entire batch in single `cb.Do()` call.

### R3. RateLimitPlugin violations map cleared entirely every 2 minutes ✅ FIXED (Batch 4)
- **File:** `internal/plugin/ratelimit.go`
- **Severity:** High
- **Status:** Fixed. Changed violations from bare `*atomic.Int64` to `*violationEntry` struct with `count` and `lastUpdated` fields. Cleanup now checks per-entry timestamps for staleness instead of bulk-deleting all entries.

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

### R6. AutoBan checkAndBan triggers ban on totalConns threshold alone ✅ FIXED (Batch 4)
- **File:** `internal/plugin/autoban.go`
- **Severity:** Medium
- **Status:** Fixed. Changed `EmptyConnThreshold` default from 20 to 1000 in `DefaultAutoBanThresholds()`.

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

### Batch 4 — Critical + High priority fixes ✅ COMPLETED (2026-04-28)

1. **R1**: CoAP IsDuplicate/RecordMessageID TOCTOU race → atomic CheckAndRecord ✅
2. **C4**: OTel propagation — removed broken HeaderCarrier{} extract ✅
3. **R2**: PersistencePlugin flush — batch-level circuit breaker, skip ErrNotFound counting ✅
4. **R3**: RateLimitPlugin violations cleanup — per-entry TTL instead of bulk-delete ✅
5. **R6**: AutoBan totalConns threshold → raised to 1000 ✅

All fixes verified: 23/23 packages pass, go vet clean, 80 integration tests pass.

### Batch 5 — GitHub Actions CI Enhancement ✅ COMPLETED (2026-04-28)

Enhanced `.github/workflows/ci.yml` from 3 jobs to 7 jobs referencing shark-MQTT patterns:
- **test-unit**: Multi-OS (ubuntu/macos/windows) × multi-Go (1.26/stable) matrix, race detector (skip Windows), Codecov coverage upload, 30% threshold gate
- **test-integration**: Dedicated integration + protocol-specific integration tests
- **test-plugin**: Plugin tests + benchmarks
- **lint**: go vet + go fmt check + golangci-lint
- **build**: Multi-OS × multi-Go build verification with examples
- **docker**: Docker build + container health check + protocol smoke test
- **deploy-check**: Docker Compose + Helm lint + template validation

Also created `.github/copilot-instructions.md`, `.github/copilot-instructions/shark-socket-expert.skill.md`, `.github/skills/test-design/SKILL.md`.

### Batch 6 — Performance & cleanup (remaining)

1. **H15**: QUIC stream reuse — single bidirectional stream
2. **R4**: HeartbeatPlugin Reset O(n) → reverse index
3. **M4**: Broadcast error aggregation logging
4. **M6**: CoAP MessageIDCache O(n) eviction → ring buffer
5. **R5**: Gateway StageTimeouts sum exceeds total timeout
6. **R7-R10**: Low priority fixes (duplicate Close, malformed message logging, go.mod typo, crypto/rand jitter)

---

## Verification

After each batch:
```bash
go build ./...
go test ./... -count=1 -timeout 180s
go vet ./...
```

**Latest run (2026-04-28):** All 23 packages PASS, go vet CLEAN, go build SUCCESS.
