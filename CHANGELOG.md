# Changelog

All notable changes for `shark-socket` are recorded here.

This project uses semantic versioning. Pre-release tags use the form
`vMAJOR.MINOR.PATCH-rc.N`.

## Unreleased

### Security Hardening — Audit Batch (2026-09-02)

全量代码审计（传输/插件/运行时/LwM2M/CoAP/部署四路并行复核）后的修复批次：

- **插件生命周期接入 Gateway**：`AutoBan/RateLimit/Heartbeat/Cluster` 的 `Start/Stop`
  此前无人调用（Heartbeat/Cluster 挂载后完全失效、AutoBan/RateLimit 清理永不运行、计数随远端
  IP 无界增长）。新增 `core.LifecyclePlugin` + `PluginRunner.StartAll/StopAll`：
  Gateway.Start 在服务器 accept 之前按优先级启动（失败回滚并拒绝启动），Gateway.Stop 在
  会话全部关闭后逆序停止；插件启动失败也会回滚已启动服务器
- **UDP/CoAP closed 会话重建**：插件/错误路径 `Close` 的明文 UDP 会话此前被同源后续报文
  静默复用（`touch()` 续命、Send 全失败、槽位永久占用）；现在按状态即时回收并重建新会话
  （重新执行 OnAccept 门控）
- **CoAP observe（RFC 7641）**：同 (resource, remote) 重复注册替换旧关系（token 轮换不再
  累积）、RST 按 token 注销观察者、CON 通知重传耗尽自动注销死订阅、每源 64/全局 4096
  订阅上限、空闲清扫豁免含观察者的会话（低频率通知不再静默断订阅）、DTLS 会话拆除清理
  观察者
- **LwM2M 注册表治理**：默认 65536 注册上限（`WithMaxRegistrations`）+ 满员时节流过期清扫
  （`ErrRegistrationLimit`，此前 `SweepExpired` 仅测试调用）；lifetime 钳制 30 天并拒绝
  溢出值（此前 `seconds*time.Second` 可 int64 溢出、注册永不过期）；`Client.Register` 透传错误
- **DTLS 配置映射**：应用配置路径（`GetCertificate` 热载、`Certificates` 恒空）此前经映射后
  DTLS 服务端无证书、握手必然失败——现桥接 `GetCertificate`；`tls_client_auth` 此前被静默
  丢弃——现数值映射并在 UDP/CoAP Accept 后按 crypto/tls 策略强制校验（存在性 + 链验证，
  不依赖 pion 内部路径，v3.1.2 客户端认证流程不可靠的互操作限制见 SECURITY.md）
- **32 位崩溃**：LengthPrefixFramer 对 ≥2³¹ 长度前缀先转 int 再比较，32 位平台负数绕过上限
  并在 `make` 崩溃——改为 uint32 域比较（单帧远程崩溃，IoT 类 32 位部署）
- **黑名单 IPv4-mapped 绕过**：精确条目与对端地址双向 IP 归一化（`::ffff:a.b.c.d`）
- **错误信息收敛**：HTTP/gRPC-Web 不再把内部/插件错误文本回显给客户端（通用状态文本 +
  服务端日志；gRPC-Web 业务 handler 错误仍经 grpc-message 透传，契约不变）
- **infra**：证书热载原子化（CA 失败不再留半更新状态）、watcher 轮询间隔防 panic、
  熔断器 Open 态不再被迟到 Failure 无限延长窗口、PubSub dropped 计数随最后订阅者清理
- **MQTT**：Start 覆盖"未连接旧 client"前先 Disconnect（旧自动重连 goroutine 不再泄漏并与
  新连接互踢）、Stop 与拨号窗口竞态（generation 校验，Stop 后不再被迟到拨号复活）、
  Publish 防御性拷贝
- **文档校准**：SECURITY.md（DTLS/mTLS 支持与限制、插件生命周期、WS Origin 默认拒绝、
  黑名单动态添加不实声明移除、攻击面矩阵补订阅/注册治理行）、PLUGIN.md（生命周期插件
  章节）、docker-compose 不再向宿主机发布无鉴权 metrics/health 端口

### Zombie-Connection Reclaim (2026-08-31)

假链接/僵尸连接回收的可见性与回归保护：

- **新增 `sessions_reclaimed_total` 计数器**：TCP 读超时、UDP/CoAP 伪会话 TTL sweep、UDP/CoAP DTLS 读超时、WebSocket/gRPC-Web 读超时（PongTimeout）五个回收点全部埋点；`shared.IsTimeout` 统一判定 deadline 过期；sweep 仅在真正移除会话时计数（防与 DTLS 读超时双计）；QUIC 由 quic-go 内部 idle 处理，不经过该计数（文档注明）
- **回收回归测试**：TCP 静默对端读超时回收（含计数断言+服务器存活验证）、gRPC-Web WS 模式死对端 PongTimeout 回收（含计数断言）
- **QUIC idle 可配置**：新增 `WithQUICMaxIdleTimeout`（api 门面同步暴露），`TestQUICIdleReclaimsSilentPeer` 验证静默连接被 200ms idle 超时回收

### Test Completeness — Batch 4 (2026-08-31)

压力测试收尾（C10）：

- **非 TCP 压力测试**：新增 UDP 持续吞吐（96.8k msg/s、0 失败 + 伪会话 TTL 回收断言）、WebSocket churn（5k 循环/s、0 失败 + 会话零泄漏断言，linger(0) 拨号防 Windows 临时端口耗尽）、HTTP 并发请求（115.7k req/s、0 失败 + 按请求会话不累积断言）
- 最终总覆盖率 **78.6%**（专项补测前 75.0%）

### Test Completeness — Batch 3 (2026-08-31)

接线与广度补强（C8/C9/B6）：

- **连接上限/接受速率接线（C8）**：`MaxConnections`/`AcceptRate` 此前存在于 TCP/WebSocket/gRPC-Web/QUIC 选项并已接线 Acceptor，但**无任何 setter 与 api 门面**（配置不可达）——补 4 组 `WithMaxConnections`/`WithAcceptRate` + 8 个 api 包装；端到端测试验证 TCP 超限即断、TCP 速率突发拒绝、WS/gRPC-Web 503、QUIC 连接被服务端关闭
- **跨协议矩阵扩展（C9）**：插件链 OnMessage 矩阵从 3 协议扩到 **7 协议**（新增 CoAP/HTTP/gRPC-Web/QUIC）；新增 OnAccept 拦截矩阵——黑名单插件在 7 协议上行为一致（TCP/QUIC 断连、UDP/CoAP 无响应、HTTP/gRPC-Web 403、WS 升级后关闭）
- **低覆盖项（B6）**：`cache.StartSweeper`/`Delete`、Blacklist CIDR 分支、`Persistence.SetLogger` 错误日志路径全部补测
- **非 staged Stop（B6）**：5 个传输绕过网关直接 Start/Stop 的路径（此前 0%）全部补测，Stop 后监听器不再接受连接

### Test Completeness — Batch 2 (2026-08-31)

历史 V7/V8 修复无回归测试 + Cluster 消息路径的专项补齐：

- **HTTP OnAccept 失败双 OnClose（V8 P2-4）**：拒绝插件 → 403，已接受插件仅获一次 OnClose（链回滚），会话不泄漏
- **CloseAll 停机中途注册 + ctx 取消（V8 P2-1）**：Close 注册后继会话的 drain 循环测试；取消上下文立即中止且不关闭会话
- **gRPC-Web 错误路径 trailer 后停写（V8 P3-7）**：handler 错误响应仅含帧序列（grpc-status 13 trailer），严格帧解析器保证 trailer 后无多余字节
- **UDP getOrCreateSession OnAccept 失败分支**：拒绝插件 → 无响应、无会话、OnClose 零调用
- **Cluster 消息路由**：`handleClusterMessage` 31.2% → **100%**（畸形消息告警丢弃、wrong-topic/空载荷静默丢弃、Broadcast 错误告警）

### Test Completeness (2026-08-31)

针对协议连接管理与插件路径的测试缺口专项补齐（详见 `docs/reports/PROJECT-REVIEW-260809-091049.md` 后的逐项审计）：

- **UDP DTLS 应用数据路径首次被测试驱动**：`handleDTLSConn` 覆盖率 0% → 75.6%。旧 idle-timeout 测试空转通过（pion/dtls v3 惰性握手——客户端不写数据就永远不会有会话），已修复为"先写后断言"；新增完整 DTLS echo + 断开回收测试；测试证书从 RSA 换为 ECDSA P-256（DTLS 1.3 拒绝 RSA 证书）
- **handler panic 回归测试**（V8 P1-2 修复此前无测试）：UDP/CoAP/WebSocket 各新增 panic 隔离集成测试（对齐 TCP 已有测试）——首次调用 panic、后续请求正常服务证明进程存活
- **LwM2M update/discover 命令**：`handleUpdate` 0% → 88.9%、`handleDiscover` 0% → 100%；E2E 生命周期测试补 update 交换
- **插件后台清扫**：`RateLimit.sweep` 0% → 84.6%、`AutoBan.sweep` 0% → 100%（过期计数/封禁清理行为测试）

### Performance & Resource Improvements (2026-08-26)

- **RateLimit 分片锁**: 单一全局 Mutex 改为 32 分片（maphash 进程随机种子选择分片，防攻击者构造同分片碰撞放大锁竞争）；8 核并行微基准 243ns → 75.8ns（3.2x 扩展），单插件 TCP echo 延迟 53.5µs → 51.5µs、每消息分配 10 → 7
- **插件 per-message 键分配消除**: RateLimit/AutoBan 的 remote-IP 键首次计算后缓存到 session meta，每消息的 SplitHostPort 字符串分配降为每会话一次
- **DTLS 读缓冲从 64 KiB 降到 16 KiB（可配置）**: 每个 DTLS 连接持有独立读缓冲，旧默认下 1 万 DTLS 对端仅读缓冲即 ~640MB；新增 `WithUDPDTLSReadBufferBytes` / `WithCoAPDTLSReadBufferBytes`（api 门面同步暴露），明文 UDP 共享缓冲仍为 64 KiB
- **基准缺陷修复**: FullChain 系列基准的 `NewAutoBan(100)` 在第 100 条消息即断开连接导致 EOF 失败（并因 WS 基准无读超时而挂死整套基准）——阈值改为 `math.MaxInt` 以测量纯计数开销；WS 基准补 `SetReadDeadline`，失败快速报错
- 新增 `internal/plugin/benchmark_test.go`（RateLimit 单对端/多对端并行微基准）与分片隔离、键缓存单元测试

### CI Hardening (2026-08-09)

- **docker-build**: builds with the default Go module proxy
  (`--build-arg GOPROXY=https://proxy.golang.org,direct`) instead of the
  Dockerfile's China-oriented goproxy.cn, and now smoke-tests the image
  (runs the container and checks `/healthz`), validating the 0.0.0.0 bind.
- **permissions**: adds `actions: write` (required for `actions/upload-artifact`;
  with only `contents: read` every other scope, including actions, is denied
  and the log uploads 403).
- **lint fixes**: `worker_pool.stop` holds the submit lock across the drain
  (no empty critical section, SA2001); `MemoryMetrics.ObserveHistogram` /
  `MemoryLogger.append` append to the same slice (appendAssign);
  `Cluster.Close` returns `p.Stop()` (errcheck).
- **action upgrades**: `actions/checkout@v5`, `actions/setup-go@v6`,
  `actions/upload-artifact@v6`, `golangci/golangci-lint-action@v9` — all
  Node 24 runtimes (clears the Node 20 deprecation warnings).
- CI is green end-to-end: 7/7 jobs pass with no Node 20 annotations.

### V8 Audit Fixes (2026-08-09)

Post-V7 verification review (`docs/reports/PROJECT-REVIEW-260809-091049.md`).
All V7 fixes confirmed correct; 21 new findings fixed (1 P0 / 2 P1 / 7 P2 / 11 P3).

- **P0** — PubSub dropped counter is written under the exclusive lock (it was
  under `RLock`, causing `concurrent map read and map write` / process crash
  under drops).
- **P1** — `metricSessionManager.CloseAll` (the default gateway shutdown path)
  and the UDP plain readLoop now isolate session/handler panics like every
  other path.
- **P2** — `CloseAll` aborts on context cancellation; gRPC-Web `Stop` no longer
  hangs (Drain is a no-op, sessions are closed before waiting on goroutines);
  TCP high-water threshold clamped to ≥1 for small queues; HTTP gates plugin
  `OnClose` on an `accepted` flag; `run_stress` reconnect mode and
  `TestStressTCPBurst` get the per-connection fixes; AutoBan semantics
  documented as a strict message-count limiter.
- **P3** — TLV 32-bit length overflow guard; worker-pool nil-session guard;
  `WithReadTimeout(0)` disables the timeout; CoAP retransmission keyed by
  `(remote, msgID)` with sends outside the lock and track-after-send;
  gRPC-Web error path stops writing after the trailer; `Cluster.WithTopic`
  locked; heartbeat sweep panic-isolated; PluginChain copy-on-write snapshot
  (zero per-message allocation); Helm NOTES port-forward fixed; benchmark
  resource gate fails open.

### V7 Audit Fixes (2026-08-09)

Full audit (V7, `docs/reports/PROJECT-REVIEW-260808-220224.md`) — all
43 findings fixed (2 P1 / 16 P2 / 25 P3).

#### P1
- **Acceptor sub-1 rate**: `AcceptRate < 1` (e.g. 0.5) previously rejected every
  connection because the token bucket was capped below one token; it now caps at
  `max(rate, 1)`.
- **NetworkPolicy egress**: `egress` now allows all destinations (empty peer
  rule); the previous `namespaceSelector: {}` only matched in-cluster pods and
  silently blocked outbound connections to external endpoints.

#### Reliability & metrics
- **Session metrics**: `SessionManager.Unregister` now reports whether a session
  was actually removed, so `sessions_closed_total` is no longer double-counted
  on graceful shutdown or inflated by no-op unregisters.
- **PluginChain**: callbacks run off the read lock against a snapshot, so a
  plugin calling `SetLogger`/`Append` from inside a hook no longer deadlocks.
- **Worker pool**: `submit` is serialized against `stop()` so no task is lost at
  shutdown.
- **Gateway.Stop**: non-staged servers and the final `CloseAll` get the
  configured `CloseSessions` deadline instead of hanging on a context without a
  deadline.
- **Handler panic isolation**: a new `shared.CallHandler` recovers user
  handler/responder panics across TCP/UDP/CoAP/QUIC/WebSocket/gRPC-Web, and
  `SessionManager.Broadcast`/`CloseAll` recover session panics — a panicking
  handler can no longer crash the process.
- **TCP write queue**: `WriteQueueHighWater` now enforces early backpressure
  (default 0.8) instead of being a no-op.

#### Transports
- **DTLS (UDP/CoAP)**: sessions get a `SessionTTL` read deadline so silent peers
  are reclaimed, and `OnClose` fires exactly once via a guarded cleanup even
  when `CloseSessions` races the connection handler.
- **CoAP**: dedup map records CON messages only (NON/ACK/RST no longer
  misclassify later CON requests); a NON observe registration receives the
  current value as an initial notification; CON observe notifications are
  retransmitted until ACKed/RSTed (RFC 7641/7252).
- **WebSocket / gRPC-Web**: pong-handler + per-read deadline implement the
  previously dead `PongTimeout` (dead-peer detection); gRPC-Web WebSocket mode
  gains `PingInterval`/`PongTimeout` options and a ping loop.
- **UDP/CoAP session IDs**: allocated before publication, removing a data race.
- **RawFramer**: zero-value writes default to the same 32 KiB cap as reads.
- **gRPC-Web trailers**: `SendTrailers` sets `content-type` so a trailer-only
  response is not labelled `text/plain`.

#### Plugins / infra
- **AutoBan**: a session that trips the ban threshold is closed immediately.
- **RateLimit**: only accepted requests are recorded, bounding per-key memory
  under flood.
- **PubSub**: drops are counted per topic (`Dropped`) instead of silent, and the
  topic key is removed when the last subscriber cancels.
- **MessageLog**: `Len` skips short keys; `Replay` runs callbacks outside the
  lock so a re-entrant `Append` cannot deadlock.
- **MemoryCache**: expiry is checked under the lock; new `StartSweeper` bounds a
  long-lived cache.
- **MemoryMetrics/MemoryLogger**: retained window is capped.
- **MQTT**: `Start`/`Subscribe` honor the caller context (abort on cancellation)
  with a hard timeout.

#### Plugin lifecycle API
- `NewHeartbeat(manager, timeout, interval)` and
  `NewCluster(nodeID, bus, manager, buffer)` take their parameters at
  construction; `Start() error` / `Stop() error` are now uniform across all
  lifecycle plugins (Heartbeat, Cluster, AutoBan, RateLimit).

#### Protocol codecs
- **LwM2M TLV is now OMA-compliant**: `EncodeTLV` emits proper "Resource with
  Value" records (type byte packs TT/identifier-width/length-width flags,
  variable-width 8/16/32-bit identifiers and 8/16/24/32-bit lengths), so the
  output is parseable by real LwM2M devices. `DecodeTLV` returns raw values;
  `DecodeTLVTyped(data, resolver)` resolves data types from the object model,
  matching how devices interpret TLV (types are not carried on the wire).

#### Scripts / deploy
- **run_stress**: cloud profile gets a real resource gate (skips under memory /
  load pressure); TCP clients get a per-receive read timeout so a half-dead peer
  cannot hang a run; burst mode uses one dedicated client per goroutine.
- **run_tests**: race mode strips a pre-existing `CGO_ENABLED` so race detection
  cannot silently no-op.
- **K8s/Helm**: deployments wire `SHARK_*` env via `envFrom` the ConfigMaps
  (previously dead config); the K8s ConfigMap keys are now valid env var names.
- **Docker**: the runtime image binds `0.0.0.0` so EXPOSE'd ports are reachable.
- **Helm**: the Deployment references the ServiceAccount via the
  `serviceAccountName` helper, fixing `helm install <release>` with a custom
  release name.

### V6.1 CI Hardening (2026-08-07)

- **Go toolchain 1.26.5**: Bump `go.mod` from 1.26.4 to 1.26.5 to fix
  GO-2026-5856 / CVE-2026-42505 (crypto/tls ECH privacy leak, fixed in
  go1.26.5). `govulncheck` now reports 0 called vulnerabilities.
- **golangci-lint v2.12.2**: The pinned v1.64.2 was built with Go 1.24 and
  could not analyze a Go 1.26 module; bump to v2.12.2 (0 issues locally).
- **Observability**: NewGateway installs a metrics decorator over the session
  manager, so every transport now emits `sessions_active` (gauge) and
  `sessions_accepted_total` / `sessions_closed_total` counters through the
  configured metrics backend. The Prometheus `/metrics` endpoint is no longer
  empty during normal operation.

### V6 Audit Fixes (2026-08-06)

#### Protocol Correctness
- **CoAP option encoding:** Extended option delta/length (nibble 14, value >= 269)
  is now encoded as `value-269` to match the decoder, fixing corrupted option
  numbers/values in that range. Reserved nibble 15 is rejected as malformed and
  option-number overflow is guarded.
- **CoAP observe:** `SendObserveNotification` encodes the Observe option with the
  same variable-length `encodeObserveSeq` used by the server notification path.

#### Concurrency / Lifecycle
- **Plugins (AutoBan/RateLimit/Heartbeat/Cluster):** Replaced the unsafe
  "reassign `sync.Once{}`" restart pattern with a `lifecycle` that owns a fresh
  WaitGroup per Start cycle. Concurrent Start/Stop no longer races on the Once
  struct and the WaitGroup is never reused across cycles (previously panicked
  "WaitGroup is reused before previous Wait"). Cluster Start/Stop fields are
  mutually excluded.
- **OnClose semantics:** Transports no longer call plugin `OnClose` for sessions
  that were never accepted (Register/OnAccept failure), eliminating double
  notifications after the plugin chain's rollback.
- **TCP worker pool:** Blocking submit also selects on the session context, so a
  peer disconnect unblocks the submitting goroutine instead of leaking it until
  the pool stops.
- **SetLogger:** PluginChain and Persistence SetLogger take a lock; Persistence
  reads go through a `loggerRef()` helper.

#### Functional Fixes
- **AutoBan:** `Record` is now called from `OnMessage` (it was dead code), so
  AutoBan can actually ban; `sweep` only removes counters idle for
  `banDuration` instead of resetting every non-banned IP each cycle.
- **QUIC:** Short `stream.Write` results are retried instead of silently
  dropping the remainder of the payload.
- **MQTT:** The package-global `clientFactory` is now a per-adapter field
  (reentrancy); `Start` double-checks under the lock so concurrent Starts
  discard a duplicate connection, and connect-failure paths disconnect.
- **MessageLog.Replay:** Skips keys shorter than the 8-byte sequence prefix
  instead of panicking.
- **HTTP:** `responseRecorder` no longer buffers the response body into memory.
- **SlowHandler:** Threshold <= 0 passes requests through instead of logging
  every request as slow.
- **BoltStore:** Operations run with the closed check under the read lock,
  closing the TOCTOU window that surfaced `bolt.ErrDatabaseNotOpen`.
- **Gateway:** Removed a redundant `started` reset in `Stop`; `Health` reports
  uptime only while started.

#### Test Infrastructure & CI
- `run_tests.go`: `-mode deploy` now validates only `./tests/deploy`;
  `-mode cover` covers production packages only; integration serializes with
  `-p 1`; JSON arg splicing cleaned up.
- Stress tests use SO_LINGER(0) clients and retry transient connects, fixing
  Windows ephemeral-port exhaustion that made integration/coverage flaky.
- CI race job uses the scripted runner so it covers `./tests` and
  `./tests/stress` like local validation.
- Deployment artifacts: Helm service declares `protocol: TCP`; `.gitignore`
  anchors `/shark-socket` so it no longer hides the Helm chart directory.

### V5 Audit Fixes (2026-08-06)

#### Crash / Concurrency
- **TCP/QUIC session:** `Close()` no longer closes `writeCh`; `Send()` and `writeLoop` select on `ctx.Done()`, eliminating the "send on closed channel" panic when `Send` races with `Close`.
- **TCP worker pool:** Never closes the task queue; workers terminate via a `done` channel and drain remaining tasks on `stop()`, so a blocking `submit` can no longer race `stop()` into a panic.
- **Gateway:** `Start`/`Stop`/`Register` are serialized by a shared mutex; `Start` rejects double start; `Stop` marks not-started up front (readyz reports not-ready during shutdown) and clears stale uptime.
- **SessionManager.Broadcast:** Sends to a snapshot instead of under the read lock, so a blocking or re-entrant `Send` cannot deadlock the manager.
- **PluginChain.OnAccept:** On failure, already-accepted plugins receive `OnClose` (reverse order) to release their resources.

#### Transport
- **UDP/CoAP session wedge:** Error paths now call `closeSession` (removing the session and unregistering it) instead of only `sess.Close()`, so a peer is not permanently wedged to a closed session.
- **CoAP:** Empty-payload requests (standard GET) now reach the Handler/Responder instead of being dropped by a `len(payload) > 0` gate.
- **gRPC-Web:** Raw request bodies without gRPC-Web headers are passed through untouched; only declared gRPC-Web requests are parsed as frames.
- **TCP/QUIC read timeouts:** New `ReadTimeout` option (default 5m) closes idle connections/streams, mitigating slowloris-style resource exhaustion.
- **Framers:** Zero-value `LengthPrefixFramer`/`LineFramer` fall back to a 1 MiB safe default instead of allowing up-to-4 GiB allocations or unbounded line growth.

#### LwM2M / Protocol
- **LwM2M Server.Write:** Invokes `OnWrite` after releasing the lock (re-entrancy deadlock fix) and no longer performs callback I/O under the global mutex.
- **LwM2M TLV:** 5-7 byte integers decode from all bytes (no truncation); 4-byte float32 values decode correctly instead of 0.0.

#### CI & Test
- All regression tests added pass under `go test -race`.

### V4 Audit Fixes (2026-06-26)

#### Plugin Lifecycle
- **Cluster:** Fixed double-close panic — `Stop()` now uses `sync.Once` for channel closure. `Start()` resets `stopOnce` and recreates `stop` channel on restart, matching RateLimit/AutoBan/Heartbeat pattern.

#### Transport Lifecycle
- **HTTP/WS/gRPC-Web/CoAP/UDP:** Reset `started` flag on listen failure (port conflict, permission denied, address resolution). Previously `started` remained true, permanently blocking recovery.
- **CoAP/UDP:** `session.Close()` now captures and returns close errors from the underlying DTLS/UDP connection.
- **HTTP:** Added `sync.Once` to `session.Close()` for double-close safety (matching all other session types).

#### Performance
- **SessionManager.Range():** Inlined map iteration under RLock — avoids full `[]Session` slice allocation on every `Range()` call. `CloseAll` and `Heartbeat.Sweep` use explicit `Snapshot()` for mutating operations.
- **CoAP dedup:** Replaced `fmt.Sprintf` per-message key with struct key `{remote, msgID}` — zero allocation on hot path.

#### Configuration
- **Env vars:** Added `SHARK_TCP_TLS_MIN_VERSION` and `SHARK_QUIC_TLS_MIN_VERSION` environment variable support.

#### CI & Observability
- **golangci-lint:** Pinned to v1.64.2 (was `latest`, producing non-deterministic builds).
- **Mosquitto health check:** Changed from `nc -z` to `mosquitto_sub` in CI service containers.
- **App:** Health/metrics HTTP `ListenAndServe` goroutines now tracked via `serveWG sync.WaitGroup`.

#### Documentation
- **V4 Review:** Added `docs/reports/PROJECT-REVIEW-260626-163000.md` — comprehensive 4-agent parallel audit (26 findings: 7H/12M/7L, 0 Critical).

### Comprehensive Quality Hardening (2026-06-26)

#### V1 Interface Cleanup (Breaking)
- Removed legacy `Store` interface (error-discarding `Save`/`Load`/`Delete`).
- Renamed `StoreV2` → `Store` (all methods return errors + `List`/`Close`).
- Removed legacy `Persistence` plugin (V1). Renamed `PersistenceV2` → `Persistence`.
- Removed `BoltStore` V1 wrapper methods (`Save`→`SaveV2` internally, etc.).
- Removed `api.PersistenceV2Plugin` and `api.StoreV2` type aliases.
- Updated all tests, benchmarks, and docs to use unified interfaces.

#### Concurrency & Safety Fixes
- **PubSub (Critical):** Fixed send-on-closed-channel panic — `Publish` now holds `RLock` during iteration+send, preventing concurrent `cancel()` from closing subscriber channels.
- **PrometheusMetrics:** Fixed label slice data race — `IncCounter`/`ObserveHistogram` now copy labels instead of reusing backing arrays.
- **Cache:** Fixed `Get()` TOCTOU race — expired items are no longer deleted inside `Get`; `Sweep` handles cleanup.
- **MessageLog:** `Replay()` and `Prune()` now hold mutex for concurrent `Append` safety.

#### Plugin Improvements
- **RateLimit:** Replaced fixed-window counter with true sliding window (`[]time.Time` timestamp slice), eliminating 2x burst vulnerability at window boundaries.
- **AutoBan:** Replaced global sweep (clear all every 30min) with per-IP ban expiry (`map[string]time.Time`). Expired bans auto-removed on `OnAccept` check. Sweep interval reduced to 5min.
- **Heartbeat:** Removed `sync.Once` from `Start()` — now supports restart after `Stop()` via `running` flag + channel recreation.
- **RateLimit/AutoBan:** `Stop()` now uses `sync.Once` to prevent double-close panic. `Start()` recreates stop channel if previously closed. Added `sync.WaitGroup` for goroutine tracking.
- **Cluster:** Consume goroutine now tracked in `sync.WaitGroup`. Added broadcast amplification warning docs.
- **Persistence/Cluster:** Replaced `log.Printf` with `core.Logger` field + `SetLogger()`.

#### Transport Layer Fixes
- **HTTP/WebSocket/gRPC-Web:** Added `s.closed.Store(false)` in `Start()` to enable proper restart after `Stop()`.
- **HTTP/WebSocket/gRPC-Web:** `Serve()` goroutines now tracked in `sync.WaitGroup`.
- **HTTP:** Added `Drain()` implementation (was no-op).
- **WebSocket/gRPC-Web:** Added `ReadTimeout`/`WriteTimeout`/`IdleTimeout` to `http.Server`.
- **TCP:** Unified `Stop()` order to `StopAccept→Drain→CloseSessions` (consistent with all other transports).
- **TCP:** `writeLoop` goroutine now tracked in `connWG`.
- **CoAP:** ACK send errors now logged via `rt.Logger().Warn`.
- **QUIC:** Handler errors now logged.
- **DTLS:** Extracted shared `DTLSConfig()` to `transport/shared/`, eliminating 22-line duplication between UDP and CoAP.

#### Gateway & Runtime
- **Gateway.Stop():** Added `stopMu` mutex for concurrent `Stop()` call protection.
- **Gateway rollback:** `Start()` failure rollback now logs individual `Stop()` errors.
- **PluginChain:** Panic recovery now uses configured `core.Logger` instead of `slog` directly.
- **PluginChain:** `safeAccept/safeMessage/safeClose` converted to methods with logger access.

#### Security Hardening
- **TLS MinVersion:** Now configurable via `TLSMinVersion` field (was hardcoded TLS 1.2).
- **TLS:** `parseTLSMinVersion` rejects versions below 1.2 (1.0/1.1 are insecure).
- **Health/Metrics HTTP:** Added `ReadTimeout`/`WriteTimeout`/`IdleTimeout` to internal servers.
- **allowedOriginChecker:** Added docs warning that `"*"` wildcard is dev-only.

#### Deploy & CI
- **CI Actions:** Fixed versions — `checkout@v4`, `setup-go@v5`, `golangci-lint@v6`, `upload-artifact@v4`.
- **CI:** Added `docker-build` job for image validation.
- **Dockerfile:** Added `ENV GOTOOLCHAIN=auto` for Go toolchain compatibility.
- **docker-compose:** Added Mosquitto config mount with security notes.
- **Helm:** Added `configmap.yaml` and `serviceaccount.yaml` templates.
- **K8s:** Added `namespace: shark-socket` to Deployment and Service for consistency.
- **.dockerignore:** Added `.env`, `.env.*`, `*.pem`, `*.key` exclusions.
- **.gitignore:** Added `.claude/` exclusion.

#### Documentation
- Updated `ARCHITECTURE.md` directory tree (application/→app/, infrastructure/→infra/, core file list).
- Updated `CONTRACTS.md` Protocol type, PluginRunner methods, file references.
- Updated `PLUGIN.md` §9 from `PersistenceV2Plugin` to `Persistence Plugin`.
- Updated `README.md` coverage number, feature matrix, plugin list.
- Added `ARCHITECTURE-ANALYSIS-260626.md` — comprehensive architecture analysis.
- Added `ARCHITECTURE-METHODOLOGY-260626.md` — design decisions and methodology.
- Updated `PROJECT-REVIEW-260626-V3.md` — V3 audit with 22 findings.
- Updated `PROJECT-REVIEW-260626-230000.md` — V2 audit with 31 findings.

### Protocol Test Coverage (2026-06-15)

#### Port Exhaustion Fixes (Windows)
- Added `WithClientLinger(0)` option to TCP client — sends RST on close, avoiding TIME_WAIT.
- Added `lingerTransport()` helper for HTTP/gRPC-Web benchmarks — `SetLinger(0)` via `DialContext`.
- Added `portCooldown()` to `run_benchmarks.go` — 3s wait between groups on Windows.
- Added `integration_helpers_test.go` to http/grpcweb/websocket packages — `init()` replaces `http.DefaultTransport` and websocket `DefaultDialer` with Linger(0) dialer.
- Fixed `TestGatewayTCPRestartKeepsSessionManagerUsable` — relaxed port reuse check for fast recycling.
- Fixed `parse_test_log_test.go` — updated timestamp assertion for millisecond-free format.
- All 27 network benchmarks now pass on Windows with zero port exhaustion failures.

#### Benchmark Structural Improvements
- Added `concurrentClientsForOS()` — platform-aware concurrency caps (Windows: 50, Linux: 500).
- Added `BENCH_MAX_CONNS` env var to override concurrency levels.
- Added read deadlines to UDP and WebSocket concurrent benchmarks.
- Unified HTTP client timeout to 5s across all single-connection benchmarks.
- Fixed gRPC-Web error handling — `io.ReadAll` and `Body.Close` errors now checked.
- Fixed `BenchmarkTCPEcho_Concurrent` — each goroutine now creates its own dedicated client (was shared, causing data corruption).
- Fixed `BenchmarkWSEcho_Concurrent` — each goroutine creates its own WebSocket connection.

#### Benchmark Architecture
- Extracted shared `echoHarness`, `echoHandler`, `newEchoHarness`, `getAddr` helpers.
- Added `newEchoHarnessWithPlugins` for plugin benchmarks.
- Added `skipIfShort()` for fast smoke-test mode.
- Refactored PayloadSize (6) and Concurrent (5) benchmarks — server created once, shared across sub-benchmarks (44→11 server creations).
- Refactored single-echo (6) and plugin (4) benchmarks to use `echoHarness`.
- Added PluginChain UDP/WS benchmarks (6 new: Blacklist/RateLimit/FullChain × UDP + WS).
- Added `BenchmarkQUICEcho_Concurrent` (documented with Skip for QUIC stream limitations).
- Fixed `payloadSizes` max: `65536→65507` (safe UDP datagram limit).

#### Orchestration
- `run_benchmarks.go`: added `-list` flag (17 groups), `-bench <name>` filter, extracted `allBenchmarkGroups()`.
- Replaced `validate.ps1` and `validate_deploy.ps1` with `run_tests.go -mode vet` and `-mode deploy`.
- Removed millisecond precision from all timestamp formats.

#### Documentation
- Updated README coverage to 74.9%.
- Updated CI workflow to use Go runner instead of PowerShell scripts.
- Updated all active documentation references to use `run_tests.go` commands.

### Review Fixes (2026-06-02 Evening)
- Fixed TCP RawFramer fuzz behavior for empty raw payload reads.
- Fixed LwM2M TLV fuzz tests after field rename and added value length validation.
- Fixed PowerShell validation scripts so native command failures fail CI correctly.
- Fixed QUIC benchmark response handling to read the server-initiated stream.
- Revalidated local Go tests, race, coverage, deploy static tests, and cloud Docker smoke tests.

### Comprehensive Review Fixes (2026-06-02)

#### Critical (5 fixes)
- Fixed data race on `allowance` in shared Acceptor (mutex for rate limiting).
- Fixed DTLS goroutine leaks in UDP and CoAP transports (track + close connections).
- Fixed unbounded memory leak in CoAP dedup map (periodic cleanup).
- Fixed QUIC double-invoke of OnClose/Unregister (LoadAndDelete guard).
- Fixed data race on `clientCAFile` in tlsutil CertCache.

#### High (7 fixes)
- Fixed CoAP Observe sequence encoding inconsistency (variable-length BE).
- Added BoltDB closed-state guard with sync.RWMutex.
- Added BulkDeleter interface + BoltDB batch delete for MessageLog.
- Fixed TCP accept loop spin on persistent errors (100ms backoff).
- Added nil guard to PluginChain.Append (filter nil plugins).
- Panicking plugins return ErrPluginPanic instead of silently succeeding.
- Added nil guards to Gateway.Register and SessionManager.Register.

#### Medium (8 fixes)
- Added sync.RWMutex to PluginChain for thread safety.
- Fixed TOCTOU in SessionManager.Register capacity check.
- Added double-start guard to TCP Server (atomic.Bool).
- WebSocket pingLoop closes session on failure (prevent zombie).
- Added Acceptor rate limiting to gRPC-Web direct + WebSocket modes.
- Added sync.Once to gRPC-Web session.Close().
- Fixed cert watchers to use app lifecycle context instead of Background.
- Fixed parseUint64 to return error instead of silently returning 0.

#### Deployment Hardening
- Docker: ca-certificates, wget, HEALTHCHECK, UID 1000, .dockerignore.
- K8s: namespace, ServiceAccount, ConfigMap, NetworkPolicy, PDB, HPA.
- Helm: _helpers.tpl, NOTES.txt, fsGroup, serviceAccountName.
- CI: golangci-lint + govulncheck jobs, .golangci.yml config.

#### Test Coverage
- Added WebSocket TLS (WSS) integration test.
- Added gRPC-Web TLS integration test.
- Added CoAP Observe E2E tests (4 test functions).
- Fixed data race in CoAP duplicate CON test handler.

#### Cloud Validation
- ✅ Server 1 (120.76.44.233): Go 1.26.3 build, test, race, Docker deploy, client test.
- ✅ Server 2 (47.110.238.85): Go 1.26.3 build, test, race, coverage, Docker deploy, concurrent 64KB.

### Coverage Improvements (2026-06-02)
- Core package: 0% → 100% (17 tests)
- API package: 0% → 77.4% (44 tests)
- Runtime package: 64.5% → 88.2% (20 tests)
- UDP transport: 51.1% → 71.5% (20 tests)
- MQTT adapter: 0% → 59.1% (13 tests)
- Plugin package: 71.2% → 79.3% (8 tests)
- LwM2M protocol: 67% → 73.9% (5 tests)
- App package: 67.9% → 73.4% (4 tests)
- CoAP transport: 67.6% → 69.3% (4 tests)

### Latest Fixes & Enhancements
- MQTT integration: mosquitto broker in docker-compose, E2E tests pass on dual cloud servers.
- Fuzz testing: TCP framers, LwM2M TLV codec (11 fuzz tests total).
- Benchmark: gRPC-Web + QUIC benchmarks added (6 protocols covered).
- CoAP: message edge cases, option encoding, extended deltas (coverage 69% → 76%).
- UDP/CoAP: session ID allocation fix (defer NextID until confirmed new session).
- Health/metrics: error propagation via App.ServeErrors().
- K8s: explicit ClusterIP type, protocol fields on service ports.
- CI: PR branch filter, cross-platform path separators, missing strconv import fix.
- Docs: ARCHITECTURE test matrix, SECURITY Docker hardening updated.

### Security (Phase 1)
- Added TLS certificate hot-reload via file watcher and `GetCertificate` callback.
- Added DTLS support for UDP transport using pion/dtls v3.
- Wired CoAP DTLS and UDP DTLS from JSON config and environment overrides.
- Fixed TCP sentinel error to use `core.ErrWriteQueueFull` instead of raw `errors.New`.

### Resilience (Phase 2)
- Added configurable write deadlines on TCP (30s default), QUIC (30s default), and WebSocket (30s default).
- Added token-bucket accept rate limiter with atomic max-connections counter (TCP, QUIC, WebSocket).
- Changed TCP worker pool default full-policy from `PolicyBlock` to `PolicyDrop`.
- Added write buffer high-water-mark threshold configuration on TCP.

### IoT Protocol Depth (Phase 3)
- Expanded LwM2M object model with `ResourceType`, `OperationMask`, `ObjectDefinition`, `ResourceDefinition`, and `DeviceInfo`.
- Added OMA LwM2M TLV binary codec (`[type][id(2B)][length(2B)][value]`) with encode/decode/round-trip support.
- Added LwM2M object registry with operation validation in `Write()`.
- Added `discover` command to LwM2M CoAP responder.
- Added CoAP option delta encoding/decoding per RFC 7252.
- Added CoAP Observe (RFC 7641) — `ObserverRegistry` with Register/Remove/Notify/RemoveBySession, wired to LwM2M `OnWrite` callback.

### Durable Persistence (Phase 4)
- Added `StoreV2` interface with error-returning `SaveV2`/`LoadV2`/`DeleteV2`/`List`/`Close`.
- Added BoltDB-backed `BoltStore` implementing `StoreV2`.
- Added `MessageLog` — durable append-only message log with auto-incrementing sequence numbers, replay, and prune.
- Added `PersistenceV2` plugin using `StoreV2` with `OnMessage` hook appending to `MessageLog`.
- Added `SessionStore` — JSON session snapshot save/load/list/delete for restart recovery.

### Defect Fixes (2026-06-02 Comprehensive Review)

#### Critical Fixes
- Fixed data race on `allowance` field in shared Acceptor (added mutex for rate limiting).
- Fixed DTLS goroutine leaks in UDP and CoAP transports (track and close connections on shutdown).
- Fixed unbounded memory leak in CoAP dedup map (periodic cleanup goroutine).
- Fixed QUIC double-invoke of OnClose/Unregister (use LoadAndDelete for idempotency).
- Fixed data race on `clientCAFile` in tlsutil CertCache (added mutex to SetClientCA).
- Added nil guards on exported `Gateway.Register` and `SessionManager.Register`.

#### High Priority Fixes
- Fixed CoAP Observe sequence encoding inconsistency (variable-length big-endian encoding).
- Added BoltDB closed-state guard with mutex (operations return ErrClosed after close).
- Added BulkDeleter interface and BoltDB batch delete for MessageLog bulk operations.
- Fixed TCP accept loop spin on persistent errors (added 100ms backoff).
- Added nil guard and validation in PluginChain.Append.
- Panicking plugins now return ErrPluginPanic instead of silently succeeding.

#### Deployment Hardening
- Added ca-certificates, wget, and HEALTHCHECK to Dockerfile.
- Fixed Docker UID to 1000 for K8s compatibility.
- Added .dockerignore to reduce build context.
- Fixed docker-compose YAML ambiguity and added tmpfs mounts.
- Added K8s namespace, ServiceAccount, ConfigMap, NetworkPolicy, PDB, HPA manifests.
- Added Helm _helpers.tpl and NOTES.txt templates.
- Added golangci-lint and govulncheck CI jobs.

#### Test & Coverage
- Added WSS/TLS tests for WebSocket transport.
- Added TLS tests for gRPC-Web transport.
- Added CoAP Observe E2E tests (4 test functions).
- Fixed scripts: `./api/...` to `./api`, removed duplicate test from validate.ps1.
- Added comprehensive project review document.

## v0.1.0 - 2026-05-30

Release candidate for the redesigned Shark-Socket runtime gateway.

### Added

- Core runtime contracts for sessions, servers, codecs, plugins, observability, and staged shutdown.
- Gateway runtime composition with shared session management, global plugin execution, duplicate protocol rejection, readiness, health snapshots, rollback on failed start, and staged stop.
- TCP transport with length-prefix, line, fixed-size, and raw framers, a client helper, worker pool policies, runtime plugin integration, and shutdown cleanup.
- UDP transport with remote-address pseudo-sessions, TTL sweeping, runtime plugin execution, and shutdown cleanup.
- HTTP transport with plain router mode, session/plugin handler mode, request body limits, and per-request cleanup.
- WebSocket transport with binary message handling, origin checks, serialized writes, ping loop, runtime plugin execution, and shutdown cleanup.
- CoAP transport with message parse/marshal, CON ACK responses, responder hooks, pseudo-sessions, TTL sweeping, and runtime plugin execution.
- LwM2M in-memory lifecycle/resource model with registration, update, deregistration, lifetime expiry, resource read/write, and CoAP text-command binding.
- QUIC transport using `quic-go`, TLS-required startup, bidirectional stream request/response flow, runtime plugin execution, and shutdown cleanup.
- gRPC-Web transport with direct HTTP mode, binary frame parsing, framed data responses, grpc-status trailer frames, WebSocket mode, max message size limits, origin checks, runtime plugin execution, and session cleanup.
- Plugin ecosystem covering blacklist, rate limit, heartbeat, persistence, autoban, slow handler logging, and cluster pub/sub broadcast.
- Infrastructure primitives for in-memory cache, store, pub/sub, circuit breaker, in-memory observability, Prometheus metrics export, and OpenTelemetry tracing.
- Deployment baseline for Docker, docker-compose, Kubernetes, and Helm, including security contexts, resource requests/limits, liveness/readiness probes, and configurable Helm ports.
- Compile-checked multi-protocol example and examples documentation for TCP, WebSocket, CoAP/LwM2M, Prometheus metrics, and OpenTelemetry tracing.
- Validation tooling for normal, race, deploy, scripted unit/integration/benchmark/all test runs, JSON logs, parsed reports, fuzz smoke tests, and benchmark baselines.

### Validation

- `powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1`
- `go run scripts/run_tests.go -mode all -timeout 5m`
- `powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1 -Race`

### Known Scope

- Docker, Kubernetes, and Helm render checks are run when those tools are installed, and otherwise recorded as explicit skips by `scripts/validate_deploy.ps1`.
