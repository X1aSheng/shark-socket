# Project Review — shark-socket — 2026-06-02 06:45

## Executive Summary

Comprehensive review of the shark-socket v0.2.0-alpha codebase (Go 1.26.1).  
**158 tests pass, 0 failures, 2 skipped (MQTT — no broker).** `go vet` clean.  
8 defects and 5 improvements identified across CI, codec, persistence, API, and deployment layers.

---

## Defects (P0–P3)

### P0: Critical

#### 1. CI branch mismatch — `.github/workflows/ci.yml:5`

The push trigger specifies branches `shark-socket-main` and `main`, but the current default branch is `shark-socket-new-main`. PRs and pushes to this branch will NOT trigger CI validation.

**Fix:** Add `shark-socket-new-main` to the push branches list, or use `**` branches wildcard.

#### 2. CoAP `encodeOptionHeader` panics on extended format — `internal/transport/coap/message.go`

The `encodeOptionHeader()` function deliberately panics for option delta or length >= 13. While the Observe option (number 6) fits in compact encoding, any future option with larger numbers or values > 12 bytes will crash the server at runtime.

**Fix:** Return an error from the function chain instead of panicking.

### P1: High

#### 3. No tests for `SessionStore` — `internal/infra/store/session_store.go`

The SessionSnapshotStore (SaveSnapshot/LoadSnapshot/ListSnapshots/DeleteSnapshot) added in Phase 4 has zero test coverage. Core persistence path is untested.

**Fix:** Add `session_store_test.go` with CRUD, list, and delete coverage over both Memory and BoltDB backends.

#### 4. No tests for `PersistenceV2` — `internal/plugin/persistence.go`

The new V2 plugin with `OnMessage` → `MessageLog` hook path is not covered by any test. The existing `TestPersistenceWritesLifecycleEvents` only tests V1.

**Fix:** Add `TestPersistenceV2WritesLifecycleEvents` and `TestPersistenceV2AppendsMessages` in `plugin_test.go`.

#### 5. API layer missing StoreV2 and PersistenceV2 exports — `api/api.go`

Phase 4 additions (`StoreV2`, `BoltStore`, `MessageLog`, `SessionStore`, `PersistenceV2Plugin`) are not re-exported through the public API layer. Library consumers cannot use the new durable persistence features.

**Fix:** Add type aliases and constructors in `api/api.go`.

#### 6. `parseUint64` silently drops non-digit characters — `internal/infra/store/session_store.go`

The `parseUint64()` helper ignores all non-digit characters, producing `0` for any non-numeric key. While this is an internal function called only with numeric snapshot keys, the silent behavior hides bugs.

**Fix:** Use `strconv.ParseUint` or add validation.

### P2: Medium

#### 7. `.gitignore` insufficient

Only covers `logs/**` and `coverage.out`. Missing: `*.exe`, `.claude/`, `.vscode/`, `.idea/`, `*.test` binaries, `shark-socket` binary.

**Fix:** Expand `.gitignore`.

#### 8. Docker Compose deprecated syntax — `deploy/docker/docker-compose.yml`

`read_only: true` is specified as a top-level service key. In recent Docker Compose versions, this is ignored; it must be under the `deploy` section.

**Fix:** Move to correct nested path or remove (already enforced via Dockerfile `USER shark` and K8s `readOnlyRootFilesystem`).

### P3: Low

#### 9. Rate limiting locked to IP-based keys only

`RateLimit.OnMessage()` always extracts the host from RemoteAddr. No way for callers to provide a custom key function (e.g., for API-key or endpoint-based limiting).

**Fix:** Add `WithKeyFunc` option to `RateLimit` (backlog).

#### 10. `NewMessageLog.sortByteKeys` uses O(n²) bubble sort

Negligible for expected key counts (<10k), but worth noting for code quality.

---

## Improvements

### I1: Export `PersistenceV2Plugin` in API

```go
// api/api.go
type PersistenceV2Plugin = plugin.PersistenceV2

func NewPersistenceV2Plugin(s store.StoreV2, bucket string) *PersistenceV2Plugin {
    return plugin.NewPersistenceV2(s, bucket)
}
```

### I2: Export StoreV2 types in API

```go
type StoreV2       = store.StoreV2
type BoltStore     = store.BoltStore
type MessageLog    = store.MessageLog
type SessionStore  = store.SessionStore
```

### I3: Add session_store_test.go

Cover Save/Load/List/Delete over both Memory and BoltDB backends.

### I4: Add PersistenceV2 test in plugin_test.go

Cover OnAccept, OnClose (V2), and OnMessage→MessageLog append.

### I5: Expand .gitignore

Add entries for IDE files, binaries, Claude worktrees.

---

## Test Suite Results

| Suite       | Passed | Failed | Skipped |
|-------------|--------|--------|---------|
| Unit        | 152    | 0      | 2       |
| Integration | 6      | 0      | 0       |
| Benchmark   | 11     | 0      | 0       |
| **Total**   | **169**| **0**  | **2**   |

Skipped: MQTT adapter tests require an external broker.

### Benchmark Highlights (local Windows)

| Benchmark                          | ns/op   | B/op  | allocs/op |
|------------------------------------|---------|-------|-----------|
| SessionManager.NextID              | 1.59    | 0     | 0         |
| PluginChain (5 plugins)            | 48.79   | 0     | 0         |
| CoAP MessageParse                  | 108.6   | 312   | 3         |
| CoAP MessageMarshal                | 94.85   | 304   | 3         |
| TCP Echo (length-prefix)           | 49,646  | 88    | 7         |
| UDP Echo                           | 14,895  | 112   | 6         |
| WebSocket Echo                     | 17,761  | 1,088 | 5         |
| HTTP Echo                          | 80,097  | 10,033| 101       |

---

## Files Changed Since Last Review

**Phase 3 (IoT Protocol):**
- `internal/protocol/lwm2m/model.go` — ResourceType, OperationMask, ObjectDefinition, DeviceInfo
- `internal/protocol/lwm2m/server.go` — Object registry, operation validation, OnWrite callback
- `internal/protocol/lwm2m/coap.go` — discover command
- `internal/protocol/lwm2m/codec_tlv.go` — TLV binary codec [NEW]
- `internal/protocol/lwm2m/codec_tlv_test.go` — TLV round-trip tests [NEW]
- `internal/transport/coap/message.go` — Option parsing (RFC 7252 delta encoding)
- `internal/transport/coap/observe.go` — ObserverRegistry (RFC 7641) [NEW]
- `internal/transport/coap/observe_test.go` — Observer tests [NEW]
- `internal/app/app.go` — LwM2M OnWrite → CoAP NotifyObservers wiring

**Phase 4 (Persistence):**
- `internal/infra/store/store.go` — StoreV2 interface, Memory.List
- `internal/infra/store/bolt.go` — BoltDB backend [NEW]
- `internal/infra/store/bolt_test.go` — BoltDB tests [NEW]
- `internal/infra/store/message_log.go` — Durable message log [NEW]
- `internal/infra/store/message_log_test.go` — Message log tests [NEW]
- `internal/infra/store/session_store.go` — Session snapshots [NEW]
- `internal/plugin/persistence.go` — PersistenceV2 with OnMessage hook
- `go.mod` / `go.sum` — Added go.etcd.io/bbolt v1.4.0

**Test result:** All 20 internal packages pass, `go vet` clean, no regressions.

---

## Action Plan

| # | Item | Severity | Time |
|---|------|----------|------|
| 1 | Fix CI branch name | P0 | 5 min |
| 2 | Fix encodeOptionHeader panic → error | P0 | 15 min |
| 3 | Add session_store_test.go | P1 | 20 min |
| 4 | Add PersistenceV2 test coverage | P1 | 15 min |
| 5 | Export StoreV2/PersistenceV2 in api.go | P1 | 10 min |
| 6 | Fix parseUint64 with strconv | P1 | 5 min |
| 7 | Expand .gitignore | P2 | 5 min |
| 8 | Fix docker-compose read_only syntax | P2 | 5 min |
| 9 | Update CHANGELOG.md | P2 | 10 min |
| 10 | Update README.md | P2 | 15 min |

---

*Generated by Claude Code project review on 2026-06-02.*
