# shark-socket Code Review — Defects & Improvement Plan

> Generated 2026-04-28 via comprehensive manual review of all non-test Go source files.

## Summary

| Severity | Count | Description |
|----------|-------|-------------|
| Critical | 8 | Data races, panics, completely broken features |
| High | 15 | Race conditions, data safety, resource leaks, design flaws |
| Medium | 18 | Performance, error handling, minor races |
| Low | 14 | Idiom, style, documentation |

## Fix Progress (updated 2026-04-28)

| Batch | Status | Issues |
|-------|--------|--------|
| Batch 1 | Pushed | C1, C2, C3, C4, C5, C7, C8, H2, H3, H4, H5, H6, H7, H8, H9, H10, M8, M14, L7, L8, L14 |
| Batch 2 | Pushed | C6, H11, H12, H13, H14, M1, M2, M3, M4, M7, M13, M16, M17 |
| Batch 3 | Pushed | M5, M9, M12 |
| Remaining | Open | H15, M6, M10, M11, M15, M18, L1-L14 (partial) |

**Remaining issues** are primarily performance optimizations (H15, M6, M11), dead code removal (L1, L3), or not applicable / already addressed by design (M10 has cleanup loop, M15 has no manager field, M18 is security-through-obscurity).

---

## Critical Issues

### C1. PubSub index-based unsubscribe — panic on concurrent unsubscribe
- **File:** `internal/infra/pubsub/pubsub.go:108-133`
- **Problem:** Subscribers are keyed by positional index. When one subscriber unsubscribes, the slice shifts, invalidating all subsequent indices. Concurrent unsubscribes can panic with index-out-of-range or remove wrong subscribers.
- **Fix:** Replace positional indices with unique-ID-based map keys.

### C2. AutoBan silently fails to ban IPs — key format mismatch
- **File:** `internal/plugin/autoban.go:99` + `internal/plugin/blacklist.go:70-89`
- **Problem:** `checkAndBan` passes `utils.IPToKey(ip)` to `blacklist.Add()`, but `Add()` expects raw IP strings (it calls `net.ParseIP`). `IPToKey` may return a non-parseable format, causing silent ban failure.
- **Fix:** Pass the original `ip.String()` to `blacklist.Add()`.

### C3. Cluster plugin subscription never cleaned up
- **File:** `internal/plugin/cluster.go:137`
- **Problem:** `routeSubscribe` discards the `Subscription` return value (`_, _ = p.pubsub.Subscribe(...)`), so there's no way to unsubscribe. Every `NewClusterPlugin` leaks the subscription.
- **Fix:** Store the subscription and call `Unsubscribe` in `Close()`.

### C4. OTel distributed tracing propagation completely broken
- **File:** `internal/infra/tracing/otel.go:96,178`
- **Problem:** `StartSpan` calls `t.propagator.Extract(ctx, propagation.HeaderCarrier{})` with an empty carrier. No incoming trace context is ever propagated. Every span is a root span.
- **Fix:** Extract actual propagation headers from the context/carrier before calling `Extract`.

### C5. MemoryCache double-close panic
- **File:** `internal/infra/cache/cache.go:153-155`
- **Problem:** `Close()` closes `c.stopCh` with no guard. Calling `Close()` twice panics.
- **Fix:** Use `sync.Once` or atomic bool guard.

### C6. Gateway shutdown stages 4 and 6 are no-ops
- **File:** `internal/gateway/gateway.go:277-308`
- **Problem:** `stageManagerClose` and `stageFinalize` just wait for a timeout with no actual work (`<-ctx.Done()` is just `time.Sleep`).
- **Fix:** Either add meaningful work or remove the empty stages.

### C7. IsPrivateIP can panic on nil *IPNet from ParseCIDR
- **File:** `internal/utils/net.go:43`
- **Problem:** `net.ParseCIDR` errors are discarded. If any private range string is malformed, `network` is `nil` and `network.Contains(ip)` panics.
- **Fix:** Store parsed `*net.IPNet` values as package-level variables, parsed once.

### C8. MessageConstraint type parameter is effectively `any`
- **File:** `internal/types/message.go:13`
- **Problem:** `~[]byte | ~string | ~int | ~float64 | ~struct{} | any` — `any` in a union dominates, making all other constraints dead code. `~struct{}` is likely invalid.
- **Fix:** Remove `any` from the union; use only the specific type constraints.

---

## High Issues

### H1. Session All()/Range()/Broadcast() yield sessions outside lock
- **File:** `internal/session/manager.go:153-192`
- **Problem:** These methods release the shard read lock before the caller finishes using the yielded session. Concurrent unregistration makes the session pointer dangling.
- **Fix:** Document that yielded sessions are only valid during the callback.

### H2. BaseSession.DoClose() not protected by sync.Once
- **File:** `internal/session/session.go:139-146`
- **Problem:** Comment says "Should be called via sync.Once" but the implementation doesn't enforce it. Double-close executes cleanup twice.
- **Fix:** Embed `sync.Once` directly in `BaseSession`.

### H3. Cache Get/MGet return shared []byte references
- **File:** `internal/infra/cache/cache.go:87,140-150`
- **Problem:** Returned byte slices are the internal backing array. Mutating them corrupts the cache.
- **Fix:** Copy before returning.

### H4. Store Save/Load share []byte backing arrays
- **File:** `internal/infra/store/store.go:30,37-42`
- **Problem:** Same as H3. `Save` stores the caller's slice directly; `Load` returns the internal slice.
- **Fix:** Copy on both save and load.

### H5. Metrics maps not thread-safe on first access
- **File:** `internal/infra/metrics/metrics.go:63-96`
- **Problem:** `Counter/Gauge/Histogram` methods read from maps without synchronization. Concurrent first-access causes races.
- **Fix:** Add `sync.RWMutex` protection.

### H6. Timer() leaks Prometheus histogram vectors
- **File:** `internal/infra/metrics/metrics.go:93-96`
- **Problem:** Unlike `Counter/Gauge/Histogram`, `Timer()` never caches the histogram. Every call registers a new Prometheus vector, eventually causing `AlreadyRegisteredError`.
- **Fix:** Cache histograms in the map like the other metric types.

### H7. HalfOpen circuit breaker state overwritten unconditionally
- **File:** `internal/infra/circuitbreaker/circuitbreaker.go:98-100`
- **Problem:** On failure in HalfOpen, state is set to Open via direct `Store`, overwriting a possible concurrent `Closed` transition.
- **Fix:** Use `CompareAndSwap` from HalfOpen to Open.

### H8. WithGlobalPlugins aliases caller's slice backing array
- **File:** `internal/gateway/options.go:90-92`
- **Problem:** `append(o.GlobalPlugins, p...)` can share the caller's backing array. If caller reuses the slice, the gateway's plugin list is corrupted.
- **Fix:** Copy: `append([]types.Plugin{}, o.GlobalPlugins...)`.

### H9. AutoBan EmptyConn counts every connection, not just empty ones
- **File:** `internal/plugin/autoban.go:75`
- **Problem:** `emptyConns` is incremented on every `OnAccept`. An IP making 20 legitimate connections gets banned.
- **Fix:** Either rename to `totalConns` with a much higher default, or only increment for truly empty connections.

### H10. TimeWheel scans all entries every tick
- **File:** `internal/plugin/heartbeat.go:53-72`
- **Problem:** `advance()` iterates all slots instead of just the current slot. O(n) per tick — no performance benefit over a flat list.
- **Fix:** Only check the current slot; ensure `Reset` places entries in the correct slot.

### H11. gRPC-Web WebSocket write race (no writeMu)
- **File:** `internal/protocol/grpcgw/server.go:260-301` + `options.go:155-160`
- **Problem:** `GRPCWebSession.Send()` calls `conn.WriteMessage` without a mutex. gorilla/websocket requires serialized writes.
- **Fix:** Add `writeMu sync.Mutex` to `GRPCWebSession`.

### H12. WebSocket CheckOrigin rejects all connections by default
- **File:** `internal/protocol/websocket/server.go:55-63`
- **Problem:** Empty `AllowedOrigins` causes `CheckOrigin` to return `false`, rejecting ALL connections including same-origin. A fresh server accepts zero connections.
- **Fix:** Default to allowing all origins (with a security warning in docs) or document prominently.

### H13. CoAP retransmitLoop — mutex deadlock
- **File:** `internal/protocol/coap/server.go:161-174`
- **Problem:** `sess.mu.Lock()` is held while calling `sess.Send()`, which also tries to acquire `s.mu.Lock()`. Go mutexes are not reentrant — this deadlocks.
- **Fix:** Collect messages under lock, release lock, then send.

### H14. HTTP custom status code never written to response
- **File:** `internal/protocol/http/server.go:269-279`
- **Problem:** Handler sets custom status via `sess.SetMeta("http_status", code)` but this is only read for access logging. The actual `http.ResponseWriter.WriteHeader` is never called with the custom status.
- **Fix:** Call `w.WriteHeader(status)` with the handler-set status code.

### H15. QUIC opens a new stream for every message
- **File:** `internal/protocol/quic/session.go:68-75`
- **Problem:** `OpenStreamSync` is called for every send. QUIC streams have handshake overhead. For high throughput, this creates massive overhead.
- **Fix:** Reuse streams for multiple messages.

---

## Medium Issues

### M1. TCP sendWithReconnect — race between Connect() and Close()
- **File:** `internal/protocol/tcp/client.go:88-99`
- **Problem:** `Connect()` releases the mutex then re-acquires; `Close()` can set `c.conn = nil` in between.

### M2. TCP Receive() — no protection against concurrent Close()
- **File:** `internal/protocol/tcp/client.go:103-111`
- **Problem:** `ReadFrame` called outside the mutex while `Close()` can nil the connection.

### M3. Session manager evictGlobal — silent eviction failure
- **File:** `internal/session/manager.go:87-122`
- **Problem:** If all shards are contended, `TryLock` fails everywhere and eviction silently does nothing.

### M4. Broadcast silently drops all Send errors
- **File:** `internal/session/manager.go:185-192`
- **Problem:** All `Send` errors discarded with `_ =`. No logging or error aggregation.

### M5. LogSampler.Clean() never called — unbounded map growth
- **File:** `internal/defense/sampler.go:57-68`
- **Problem:** No background cleanup goroutine. Map grows unboundedly under attack.

### M6. CoAP MessageID cache eviction is O(n)
- **File:** `internal/protocol/coap/session.go:105-124`
- **Problem:** Eviction iterates all 500 entries on every insert when full.

### M7. CoAP MessageIDCacheSize option never wired to session
- **File:** `internal/protocol/coap/session.go:109` + `options.go:21`
- **Problem:** `Options.MessageIDCacheSize` exists but `NewCoAPSession` ignores it; uses hardcoded 500.

### M8. Logger global defaultLogger — data race
- **File:** `internal/infra/logger/logger.go:278-284`
- **Problem:** `SetDefault`/`GetDefault` access `defaultLogger` without synchronization.

### M9. BufferPool totalMem is monotonic — cap check meaningless
- **File:** `internal/infra/bufferpool/pool.go:158-161`
- **Problem:** `totalMem` never decrements. Cap applies to cumulative allocations, not current memory.

### M10. ConnectionLimiter — unbounded map growth under IP rotation
- **File:** `internal/infra/ratelimit/conn_limiter.go:43-65`
- **Problem:** Every unique IP creates a permanent map entry (cleaned only after `window*2`).

### M11. RateLimit plugin CAS spin loop
- **File:** `internal/plugin/ratelimit.go:55-63`
- **Problem:** Tight CAS loop with no backoff under contention.

### M12. AutoBan counter reset every minute makes banning nearly impossible
- **File:** `internal/plugin/autoban.go:115-130`
- **Problem:** All counters reset every 60s. Requires 20 violations within one minute.

### M13. SlowQueryPlugin mutates global variable
- **File:** `internal/plugin/slow_query.go:33` + `chain.go:15`
- **Problem:** `NewSlowQueryPlugin` sets package-level `SlowQueryThreshold`. Two instances conflict.

### M14. TCP WorkerPool PolicyClose acts like PolicyDrop
- **File:** `internal/protocol/tcp/worker_pool.go:98-104`
- **Problem:** `PolicyClose` returns `ErrWriteQueueFull` instead of closing the session.

### M15. HTTP SetManager race condition
- **File:** `internal/protocol/http/server.go:249`
- **Problem:** `manager` field set without synchronization, read concurrently from handlers.

### M16. gRPC AllowedOrigins option never wired to upgrader
- **File:** `internal/protocol/grpcgw/options.go:116-118` + `server.go:58-62`
- **Problem:** `AllowedOrigins` is set but never applied to the WebSocket upgrader.

### M17. CoAP ACK sent before handler processes CON message
- **File:** `internal/protocol/coap/server.go:121-125`
- **Problem:** ACK confirms receipt+processing, but handler hasn't run yet.

### M18. generateID exposes monotonic counter in message IDs
- **File:** `internal/types/message.go:54-67`
- **Problem:** ID format: `[8 random hex][16 counter hex]`. Counter is trivially predictable.

---

## Low Issues

### L1. BackpressureController and BroadcastLimiter are dead code
- **File:** `internal/defense/backpressure.go:9-72`
- **Problem:** Defined but never used anywhere. Either wire them in or remove.

### L2. ProtocolLabel does unique.Make on every call
- **File:** `internal/types/enums.go:46-48`
- **Problem:** Interns 9 possible strings via hash lookup every call. Use pre-allocated array.

### L3. AtomicBool — use atomic.Bool (stdlib)
- **File:** `internal/utils/atomic.go:22-31`
- **Problem:** `atomic.Bool` exists since Go 1.19. Custom wrapper is unnecessary.

### L4. hashAny default case uses fmt.Sprintf
- **File:** `internal/utils/sync.go:84`
- **Problem:** Slow, allocates. Document known key types or use `hash/maphash`.

### L5. LRUList.Stop() allocates new map, leaks old nodes
- **File:** `internal/session/lru.go:85-89`
- **Problem:** Nodes in old map not returned to sync.Pool.

### L6. GatewayConfig type alias prevents type-specific behavior
- **File:** `internal/gateway/config.go:25`
- **Problem:** `type GatewayConfig = config.GatewayConfig` is an alias, not a new type.

### L7. TLSReloader Close() uses mutex — sync.Once more idiomatic
- **File:** `internal/protocol/tcp/tls_reloader.go:78-87`
- **Problem:** Mutex only protects double-close check. `sync.Once` is simpler.

### L8. acceptBackoff has custom min() — Go 1.21+ has builtin
- **File:** `internal/protocol/tcp/server.go:258-271`
- **Problem:** Shadows builtin `min`.

### L9. crypto/rand used for jitter — overkill
- **File:** `internal/protocol/tcp/client.go:175-187`
- **Problem:** `crypto/rand.Read` is blocking OS-entropy source. Use `math/rand/v2`.

### L10. LengthPrefixFramer allocates on every frame
- **File:** `internal/protocol/tcp/framer.go:52,76`
- **Problem:** `make([]byte, 4)` per frame, `append(header, payload...)` doubles memory.

### L11. OverloadProtector only monitors sessions, docs claim more
- **File:** `internal/defense/overload.go:12-20`
- **Problem:** Docs describe memory/FD/goroutine monitoring, only sessions implemented.

### L12. Go module has probable typo in dependency
- **File:** `go.mod:28`
- **Problem:** `go.yaml.in/yaml/v2` — likely typo of `gopkg.in/yaml.v2`.

### L13. config.GatewayConfig ShutdownTimeout is string not Duration
- **File:** `internal/infra/config/config.go:168-169`
- **Problem:** Consumers must parse string to Duration themselves.

### L14. QUIC handleConn type-asserts any to *quic.Conn
- **File:** `internal/protocol/quic/server.go:92`
- **Problem:** Unchecked type assertion. Accept `*quic.Conn` directly.

---

## Fix Order (Batched)

### Batch 1 — Critical fixes (safety)
1. C5: MemoryCache double-close (sync.Once)
2. C7: IsPrivateIP nil panic (pre-parsed CIDRs)
3. C2: AutoBan key format mismatch
4. C1: PubSub index-based unsubscribe
5. C3: Cluster subscription cleanup
6. C8: MessageConstraint type parameter

### Batch 2 — Data safety & races
7. H3: Cache Get/MGet copy on return
8. H4: Store Save/Load copy
9. H8: WithGlobalPlugins slice copy
10. H5: Metrics maps thread safety
11. H6: Timer histogram caching
12. H7: HalfOpen CAS fix
13. H2: DoClose sync.Once

### Batch 3 — Protocol fixes
14. H13: CoAP retransmitLoop deadlock
15. H14: HTTP custom status code
16. H11: gRPC-Web write mutex
17. H12: WebSocket origin default
18. H15: QUIC stream reuse

### Batch 4 — Performance & remaining
19. H10: TimeWheel current-slot-only
20. H9: AutoBan emptyConns fix
21. M14: PolicyClose behavior
22. M8: Logger global thread safety
23. M15: SetManager race (atomic.Pointer)
24. L1-L14: Low-priority cleanups

---

## Verification

After each batch:
```bash
go build ./...
go test ./... -race -count=1 -timeout 120s
go vet ./...
```
