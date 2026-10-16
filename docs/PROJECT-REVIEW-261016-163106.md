# SHARK-SOCKET 项目审查与改进计划

- 审查基线: main @ 2483f59bca21542603d04d66021aa2e779f0a357
- 审查时间: 2026-10-16 16:31:06
- 审查范围: 211 个 .go 文件（其中 110 个 _test.go），internal/ 约 24.4k 行
- 工具链: go1.26.5 windows/amd64, golangci-lint v2.11.3, govulncheck v1.1.4
- 文档规范: 全文仅使用基本 ASCII 字符与中文

---

## 1. 结论摘要 TL;DR

项目功能完整、测试充分，但**持续集成已连续 8 次失败**（最近一次 run #140 即当前 HEAD）。
失败并非 flaky，而是两个确定性缺陷：lint 有 5 处静态检查未通过，govulncheck 命中 11 个已知漏洞。
这两项必须在任何后续改进之前修复，否则所有提交都无法获得 CI 绿灯。

| 类别 | 数量 | 严重度 | 状态 |
| --- | --- | --- | --- |
| CI 阻断（lint 失败） | 1 项 / 5 处 | P0 | 待修复 |
| CI 阻断（漏洞扫描失败） | 1 项 / 11 漏洞 | P0 | 待修复 |
| 格式规范 | 1 项 | P1 | 待修复 |
| 结构性重复代码 | 6 项 / 约 400 行 | P1-P2 | 待分阶段 |
| 计数与一致性风险 | 2 项 | P2 | 待确认 |

CI 现状（GitHub Actions，branch=main）:

```
run #140  2483f59bc(failure)  <- 当前 HEAD
run #139  2860a0f78(failure)
run #138  629e6ca61(failure)
run #137  b63e37b4d(failure)
run #136  e86b3ae7b(failure)
run #135  4fb558b17(failure)
run #134  65eb837fe(failure)
run #133  e3d473887(success)  <- 最后一个绿灯
```

run #140 逐 job 结果（8 个 job）:

```
Validate (windows-latest)             success
Docker Build & Smoke Test (ubuntu)    success
Lint (ubuntu-latest)                  FAILURE  <- golangci-lint
Race (ubuntu-latest)                  success
Security Scan (ubuntu-latest)         FAILURE  <- govulncheck
Coverage (ubuntu-latest)              success
Validate (ubuntu-latest)              success
```

即：功能正确性已被 CI 证明（6 个 job 全绿），红色完全来自静态检查与依赖安全两项。

---

## 2. 测试执行证据

全部 7 种测试模式在本机复现，结论与 CI 功能类 job 一致。

| 模式 | 命令 | 退出码 | 结果 |
| --- | --- | --- | --- |
| unit | `run_tests.go -mode unit` | 0 | PASS |
| vet | `run_tests.go -mode vet` | 0 | PASS |
| integration | `run_tests.go -mode integration -timeout 10m` | 0 | PASS |
| deploy | `run_tests.go -mode deploy` | 0 | PASS |
| cover | `run_tests.go -mode cover -timeout 10m` | 0 | PASS, 总覆盖率 79.4%（门限 70%） |
| race | `run_tests.go -mode race -timeout 15m` | 0 | PASS |
| benchmark | `run_tests.go -mode benchmark` | 0 | PASS |

日志归档于 `logs/`，最近产出:

```
logs/2026-10-16T16-17-11_unit.log
logs/2026-10-16T16-17-27_vet.log
logs/2026-10-16T16-22-08_integration.log
logs/2026-10-16T16-24-54_cover.log
logs/coverage.out
```

集成测试关键指标（取自 integration 日志）:

```
HTTPRequests: 1037786 msg/s, P99 3.084ms, fail=0
WSChurn:       5425.7 msg/s, P99 1.1204ms, fail=0
```

结论：**功能层面无需修复**。所有缺陷集中在 CI 配置、依赖版本、格式与代码结构。

---

## 3. 缺陷登记表

### D1 (P0) lint job 失败：golangci-lint 5 处未通过

复现命令与输出（本机，与 CI 同一份 `.golangci.yml`）:

```
$ golangci-lint run --timeout 5m ./...
internal\transport\shared\pool_scalable.go:165:11: QF1008: could remove embedded field "poolCore" from selector (staticcheck)
internal\transport\shared\pool_scalable.go:267:4:  QF1008: could remove embedded field "poolCore" from selector (staticcheck)
internal\transport\shared\pool_scalable.go:285:4:  QF1008: could remove embedded field "poolCore" from selector (staticcheck)
internal\transport\shared\pool_scale_behavior_test.go:186:3: SA2001: empty critical section (staticcheck)
internal\transport\shared\pool_scalable.go:187:24: func (*ScalablePool).shouldScale is unused (unused)

5 issues:
* staticcheck: 4
* unused: 1
```

逐条分析:

| 编号 | 位置 | 规则 | 性质 |
| --- | --- | --- | --- |
| D1.1 | pool_scalable.go:187 | unused | `shouldScale()` 是死代码 |
| D1.2 | pool_scalable.go:165 | QF1008 | 多余的内嵌字段限定符 |
| D1.3 | pool_scalable.go:267 | QF1008 | 同上 |
| D1.4 | pool_scalable.go:285 | QF1008 | 同上 |
| D1.5 | pool_scale_behavior_test.go:186 | SA2001 | 空临界区 |

D1.1 详情：`shouldScale()` 的函数体（第 188-200 行）与其"调用方" `maybeScale()`（第 210-240 行）逻辑完全一致，说明其判定逻辑已被完整内联进 `maybeScale`，原函数成为遗留。全文只有第 180 行与第 203 行的注释提到它，无任何代码调用点。

```
$ grep -rn "shouldScale" internal/
internal/transport/shared/pool_scalable.go:180:// shouldScale decides whether to grow or shrink the worker set.
internal/transport/shared/pool_scalable.go:187:func (p *ScalablePool) shouldScale() int {
internal/transport/shared/pool_scalable.go:203:// maybeScale applies shouldScale's verdict.
```

影响：CI lint 全红。根因：并行开发弹性池时先写 `shouldScale` 再改为内联，未删除原函数；且 `.golangci.yml` 的 exclusions 只放行了 QF1003，未放行 QF1008/SA2001/unused。

修复方案：删除 D1.1 死函数（并把其文档注释合并进 `maybeScale`，因为那段关于"为何只用队列占用率"的设计说明有价值）；D1.2-D1.4 去掉 `p.poolCore.` 限定符；D1.5 改为对该空临界区给出显式理由或改用 `RLock` 探针，使其语义不再是"空"。

### D2 (P0) security job 失败：govulncheck 命中 11 个漏洞

复现命令与输出:

```
$ govulncheck ./...
Your code is affected by 11 vulnerabilities from 1 module and the Go standard library.
```

漏洞与修复版本对照:

| # | 漏洞号 | 受影响模块 | 当前版本 | 修复版本 |
| --- | --- | --- | --- | --- |
| 1 | GO-2026-6617 | golang.org/x/net / net/http | v0.47.0 / go1.26.5 | v0.60.0 / go1.26.9 |
| 2 | GO-2026-6613 | net/http | go1.26.5 | go1.26.9 |
| 3 | GO-2026-6612 | golang.org/x/net / net/http | v0.47.0 / go1.26.5 | v0.60.0 / go1.26.9 |
| 4 | GO-2026-6611 | golang.org/x/net / net/http | v0.47.0 / go1.26.5 | v0.60.0 / go1.26.9 |
| 5 | GO-2026-6608 | net/textproto | go1.26.5 | go1.26.9 |
| 6 | GO-2026-6607 | crypto/tls | go1.26.5 | go1.26.9 |
| 7 | GO-2026-6603 | golang.org/x/net / net/http | v0.47.0 / go1.26.5 | v0.60.0 / go1.26.9 |
| 8 | GO-2026-6165 | github.com/pion/dtls/v3 | v3.1.2 | v3.1.4 |
| 9 | GO-2026-6090 | crypto/tls | go1.26.5 | go1.26.6 |
| 10 | GO-2026-6089 | net/http | go1.26.5 | go1.26.6 |
| 11 | GO-2026-5972 | encoding/asn1 | go1.26.5 | go1.26.6 |

修复动作只有三处:

1. go.mod 的 `go` 指令 `1.26.5` -> `1.26.9`（CI 的 `go-version-file: go.mod` 会随之安装新工具链，覆盖 #1-#7、#9、#10、#11）
2. go.mod 的 `golang.org/x/net v0.47.0` -> `v0.60.0`（覆盖 #1、#3、#4、#7 的模块侧修复）
3. go.mod 的 `github.com/pion/dtls/v3 v3.1.2` -> `v3.1.4`（覆盖 #8）

已在本机验证 go1.26.9 工具链可正常下载:

```
$ GOTOOLCHAIN=go1.26.9 go version
go: downloading go1.26.9 (windows/amd64)
go version go1.26.9 windows/amd64
```

影响：CI security 全红，且确实存在可达的真实漏洞路径（例如 `udp.session.Send -> dtls.Conn.Write -> handshake.Handshake.Unmarshal`，见 govulncheck trace #8-1）。这是本次审查中安全优先级最高的一项。

### D3 (P1) scripts/workerpool_baseline.go 未通过 gofmt

```
$ gofmt -l .
scripts\workerpool_baseline.go
```

差异内容（两处）:

```
@@ -205,7 +205,7 @@
 	var (
-		metrics   *observability.PrometheusMetrics
+		metrics    *observability.PrometheusMetrics
 		metricsURL string
@@ -434,4 +434,4 @@
 var _ = binary.BigEndian
-var _ = sort.Ints        <- 无行尾换行
+var _ = sort.Ints
```

影响：仓库自身格式标准不成立。该文件带 `//go:build ignore`，不在任何 package 内，因此 golangci-lint v2 的 formatter 不会扫到它，CI 当前不因此失败；但一旦任何人执行 `gofmt -w` 或新增显式格式检查步骤即会破坏 `git diff` 干净性。属于必须清零的规范债务。

### D4 (P1) grpcweb 完全复制 websocket 的会话实现

`internal/transport/grpcweb/websocket_session.go`（91 行）与 `internal/transport/websocket/session.go`（93 行）逐行对应，唯一差异是:

- 结构体名 `session` -> `webSocketSession`
- `Protocol()` 返回值 `ProtocolWS` -> `ProtocolGRPCWeb`
- grpcweb 版把写超时抽成了 `setWriteDeadline()` 方法

字段列表完全一致（id/conn/writeTimeout/createdAt/activeAt/state/meta/writeMu/ctx/cancel/closeOnce），方法体逐字相同。

影响：两份必须在语义上保持同步的代码。任何一处修复（例如 Close 的关闭顺序、writeMu 的持锁范围）都需要人工复制到另一处，是典型的漏改源。当前已存在轻微漂移（`setWriteDeadline` 只在一侧抽取）。

### D5 (P1) 8 份会话访问器样板

每个 transport 的会话类型都重复实现同一组访问器。以 `LastActiveAt` 为例，8 个文件是逐字相同的:

```
internal/transport/http/session.go:47
internal/transport/tcp/session.go:58
internal/transport/udp/session.go:65
internal/transport/websocket/session.go:48
internal/transport/coap/session.go:65
internal/transport/quic/session.go:49
internal/transport/grpcweb/session.go:42
internal/transport/grpcweb/websocket_session.go:49
    -> func (s *session) LastActiveAt() time.Time { return time.Unix(0, s.activeAt.Load()) }
```

`SetMeta/GetMeta/DelMeta` 同样 8 份逐字相同:

```
func (s *session) SetMeta(k string, v any) { s.meta.Store(k, v) }
```

按 11 个访问器（ID/Protocol/RemoteAddr/LocalAddr/State/CreatedAt/LastActiveAt/Context/SetMeta/GetMeta/DelMeta）x 8 个类型估算，重复规模约 88 行。

### D6 (P1) touch() 6 份重复

```
internal/transport/tcp/session.go:68
internal/transport/udp/session.go:75
internal/transport/websocket/session.go:58
internal/transport/coap/session.go:75
internal/transport/quic/session.go:59
internal/transport/grpcweb/websocket_session.go:55
    -> func (s *session) touch() { s.activeAt.Store(time.Now().UnixNano()) }
```

### D7 (P1) Close() 骨架 8 份重复

`s.state.Store(uint32(core.StateClosed))` 出现在 8 个会话文件中:

```
internal/transport/http/session.go
internal/transport/tcp/session.go
internal/transport/udp/session.go
internal/transport/websocket/session.go
internal/transport/coap/session.go
internal/transport/quic/session.go
internal/transport/grpcweb/session.go
internal/transport/grpcweb/websocket_session.go
```

每份的骨架都是 `closeOnce.Do` 内 状态置位 -> cancel -> 加锁写关闭帧 -> 关闭底层连接。这是**并发正确性最关键**的一段代码，却是 8 份独立副本，任何一份的加锁顺序写错都不会被其他副本发现。

### D8 (P2) register/OnAccept/rollback 块重复约 12 处

调用点分布:

```
coap/server.go:281,285 / 779,784     (2 处)
grpcweb/server.go:186,200 / 265,274  (2 处)
http/server.go:179,193               (1 处)
quic/server.go:163,167               (1 处)
tcp, udp, websocket 各自 1-2 处
```

同一份语义有三种写法，风险在于"回滚是否调用 OnClose"这一决定很容易写错:

- http 风格（server.go:192-204）：`accepted` 标志 + `defer`，统一在 defer 里判断是否 OnClose
- grpcweb WebSocket 风格（server.go:265-283）：显式 `LoadAndDelete` + 两次内联 Unregister + Close，不调 OnClose
- grpcweb HTTP 风格（server.go:186-204）：`defer` + accepted 标志

三种写法各自都正确（已逐条核对：OnAccept 失败时插件链已回滚部分 accept，因此调用方确实不应再调 OnClose），但"正确"依赖于每一处的作者都理解了这一约定。缺少一个统一的 helper 来把这个约定固化。

### D9 (P2) app.go 中 TLS 加载块重复 4 处

```
internal/app/app.go:131-136   (tcp)
internal/app/app.go:142-147   (udp)
internal/app/app.go:169-174   (coap)
internal/app/app.go:199-204   (quic, 变体: 带 serverName)
```

四处都是 `loadServerTLSConfig(proto) -> append opts -> a.certCaches = append(...)`。第四处还多一个 serverName 参数，说明它本可以是一个带选项的统一函数。

### D10 (P2) 覆盖率 0% 的生产函数

覆盖率 79.4% 达标，但以下生产代码路径覆盖率为 0:

```
internal/transport/websocket/session.go:45  RemoteAddr  0.0%
internal/transport/websocket/session.go:46  LocalAddr   0.0%
scripts/rateclient/main.go:20               main        0.0%
scripts/rateclient/main.go:136              readFull    0.0%
scripts/parse_test_log.go:44                main        0.0%
```

其中 websocket 的 RemoteAddr/LocalAddr 属于 `core.Session` 契约方法，被调用方（日志、审计插件）实际使用但无断言。`scripts/rateclient` 的 `readFull` 是本次刚加入的帧读取修正逻辑，恰恰是最需要回归保护的新代码，却零覆盖。

### D11 (P0) 时序竞态测试：TestWorkerPoolDropPolicyRejectsWhenFull

该测试在 race 模式下失败（首次出现于依赖升级后的全量回归）:

```
worker_pool_test.go:83: second submit error = <nil>, want write queue full
--- FAIL: TestWorkerPoolDropPolicyRejectsWhenFull (0.20s)
FAIL	github.com/X1aSheng/shark-socket/internal/transport/shared	4.550s
```

原实现:

```go
	pool := NewWorkerPool(func(core.Session, core.Message) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	}, nil, 1, 1, PolicyDrop)   // 1 worker, queue depth 1
	pool.Start(1)
	pool.Submit(sess1, "one")    // 期望占满队列
	err := pool.Submit(sess2, "two")   // 期望被拒
	if !errors.Is(err, core.ErrWriteQueueFull) { t.Fatalf(...) }
```

缺陷：队列深度为 1，唯一 worker 在 `<-p.queue` 上随时可能取走第一个任务。若 worker 在第二次 Submit 之前完成出队，队列即为空，第二次 Submit 自然成功。测试把"队列已满"当作事实，但它只是一个概率。`-race` 插桩改变了调度时序，把这个潜在竞态暴露出来。

这是 CI race job 的**间歇性失败源**：同样的代码可能在绿灯与红灯之间随机摆动，属于必须消除的缺陷（与本次任务要求的"逐个测试确认"一致：先复现失败，再改，再以重复运行证明稳定）。

修复思路与前文 `TestScaleDownFloor` 同类：不依赖时序，改为由 handler 主动信号，把"队列已空"变成确定事实。修改后以 `-count=20` 各跑非 race 与 race 两轮验证稳定性。

同时记录一条环境层观察（非代码缺陷，不入修复清单）：本机首次使用自动下载的 go1.26.9 工具链跑 `-mode cover` 时，出现过一次 `toolchain@v0.0.1-go1.26.9` 下 `cmd/internal/pkgpattern is not in std` 的报错，仅影响 `examples/basic_tcp` 与 `examples/basic_lwm2m` 两个包，重跑即通过。原因是 toolchain 模块首次解压与并发包构建的竞态。CI 使用 `actions/setup-go` 安装完整发行版，不经过 toolchain 模块路径，因此不受影响。

### D12 (P2) README 的静态链接声明缺少前提

README 开篇写 "`shark-socket` is statically linked with no external runtime dependency"（中文版同）。该表述对 Docker 镜像是成立的（Dockerfile 显式 `CGO_ENABLED=0`），但对**任何装有 C 工具链的主机上的裸 `go build`** 不成立。云端验证时实测:

```
默认构建      : dynamically linked, interpreter /lib64/ld-linux-x86-64.so.2
                (依赖 libc.so.6 与 ld-linux-x86-64.so.2)
CGO_ENABLED=0 : statically linked
```

仓库中没有任何代码需要 cgo，但主机上有 gcc 时 `CGO_ENABLED` 默认为 1，cgo 版 DNS 解析器随之启用。实际后果是: 按 README 直接构建的用户会得到一个无法在 Alpine/musl 镜像上运行（除非装 glibc 兼容包）的二进制，而这恰好与紧接其后的 "deploys on resource-constrained edge nodes" 相矛盾。

已在两版 README 补上构建命令、两种产物的 `file` 输出对照，并说明 `-race` 仍需要 `CGO_ENABLED=1`，避免两个要求被混为一谈。

---

## 4. GitHub Actions 修复方案

### 4.1 判定

CI 的**功能正确性**已被证明（validate-ubuntu / validate-windows / race / coverage / docker-build 全绿）。
需修复的是两个静态 job，且均可在本机确定性复现，无需盲改。

### 4.2 修复项

| 步骤 | 目标 job | 动作 | 验收 |
| --- | --- | --- | --- |
| A1 | Lint | 删除 shouldScale 死函数（D1.1） | `golangci-lint run ./...` 的 unused 计数归零 |
| A2 | Lint | 去掉 3 处 poolCore 限定符（D1.2-D1.4） | QF1008 归零 |
| A3 | Lint | 修正 pool_scale_behavior_test.go:186 空临界区（D1.5） | SA2001 归零 |
| A4 | Lint | 修正 workerpool_baseline.go 格式（D3） | `gofmt -l .` 输出为空 |
| A5 | Security | go.mod: go 1.26.5 -> 1.26.9 | stdlib 类漏洞清除 |
| A6 | Security | go.mod: x/net v0.47.0 -> v0.61.0 (修复下界 v0.60.0) | x/net 类漏洞清除 |
| A7 | Security | go.mod: pion/dtls/v3 v3.1.2 -> v3.1.12 (修复下界 v3.1.4) | 该模块漏洞清除，govulncheck 漏洞数归零 |
| A8 | 全部 | 全量 7 模式回归 | 退出码全为 0 |
| A9 | Race | 消除 TestWorkerPoolDropPolicyRejectsWhenFull 的时序竞态（D11） | race 模式连续通过，不再随机翻转 |

### 4.3 每步的验证方式

每一项都遵循"先测试确认、再修改、再验证"的闭环:

1. 确认：用最小命令复现该缺陷（例如 `golangci-lint run` 单文件、`govulncheck ./...`）
2. 修改：只改与该缺陷相关的行
3. 验证：重跑确认命令 + 相关 package 的单元测试 + `run_tests.go -mode vet`
4. 提交：每项一个独立 commit，提交信息标注缺陷编号

### 4.4 关于 CI 触发路径的一处隐患

`ci.yml` 的 `on.push.branches` 为 `shark-socket-new-main` / `shark-socket-main` / `main`。
当前工作分支是 `main`，与配置匹配，因此推送即触发，无需改动。但三个分支配对 `pull_request` 目标不变时，若将来改默认分支名，需同步此处，否则 CI 会静默不跑。建议在本次修复中不改动，仅记录。

---

## 5. 结构性重复的改进计划（分阶段）

上述 D4-D9 合计约 400 行重复，但**涉及并发正确性最敏感的代码路径**，且用户明确要求"尽可能保持程序的可重入性"。因此不做一次性大重构，改为分阶段、每阶段可独立回滚。

### 5.1 设计：抽出一个会话基类

在 `internal/transport/shared` 新增 `session_base.go`，提供一个可嵌入的基类，承载:

- 共同字段：createdAt / activeAt(atomic) / state(atomic) / meta(sync.Map) / ctx / cancel / closeOnce
- 共同方法：ID / CreatedAt / LastActiveAt / Context / State / SetMeta / GetMeta / DelMeta / Touch（导出形式）
- 共同骨架：`MarkClosed()`（状态置位 + cancel）、`CloseOnce(fn)`（保证只执行一次）

注意点（决定此项能否保持可重入）:

1. `Protocol()` 与 `RemoteAddr()/LocalAddr()` **不**进基类，因为前者是每传输常量、后者取自不同底层连接类型（`*net.TCPConn` / `*websocket.Conn` / `*quic.Conn` / `http.ResponseWriter`），强行统一会引入类型断言或接口间接层，反而降低可读性。
2. 基类只做**状态与元数据**，不接管 `Send` 与 `Close` 的 I/O 部分。I/O 是各传输的本质差异，必须留在各自实现中。
3. 嵌入后每个会话的 `Close` 仍由本传输实现，只在开头/结尾调用基类的 `MarkClosed/CloseOnce`，从而把"加锁顺序"收敛到一处。

### 5.2 阶段划分

| 阶段 | 内容 | 影响面 | 回滚 |
| --- | --- | --- | --- |
| S1 | 新增 shared/session_base.go + 单元测试 | 新增文件，零行为变更 | 删除新文件 |
| S2 | 先迁移 grpcweb/websocket_session.go（D4），改为复用 websocket 的会话类型或基类 | 1 个包 | 单文件回滚 |
| S3 | 迁移 tcp/udp/http/quic 的访问器与 touch（D5/D6） | 4 个包 | 逐包回滚 |
| S4 | 迁移 coap（其会话有额外 DTLS 字段） | 1 个包 | 单包回滚 |
| S5 | 统一 Close 骨架（D7） | 8 个文件 | 逐文件回滚 |
| S6 | 抽出 register/OnAccept/rollback helper（D8） | 6 个 transport | 逐传输回滚 |
| S7 | 抽出 app.go 的 TLS 加载 helper（D9） | 1 个文件 | 单文件回滚 |

每一阶段的验收标准相同：`run_tests.go` 的 unit + integration + race 三模式全绿，且覆盖率不低于当前 79.4%。

### 5.3 前置约束

- 阶段 S1-S7 必须在 A1-A8 全部完成、CI 恢复绿灯之后开始，否则无法区分"新引入的失败"与"既有的红"。
- 每个阶段一个 commit，阶段内不夹带其他变更。
- 不改变任何导出的语义契约（`core.Session` 的 13 个方法签名与行为不变）。

---

## 6. 可重入性保持说明

本次所有修复动作对可重入性的影响评估:

| 动作 | 对可重入性的影响 |
| --- | --- |
| 删除 shouldScale 死函数 | 无（无调用点） |
| 去掉 poolCore 限定符 | 无（同一方法，同一接收者） |
| 修正空临界区 | 无（仅测试代码） |
| 修 workerpool_baseline.go 格式 | 无（该文件 `//go:build ignore`，不参与构建） |
| go.mod 版本升级 | **需验证**：升级 stdlib 与 x/net/dtls 可能改变并发原语实现。验收标准为 race 模式全绿 + 集成测试全绿，以此证明无引入数据竞争 |
| 抽取会话基类（S1-S7） | **正面收益**：把 8 份独立的状态机收敛为 1 份，重复的加锁顺序错误风险下降 |

明确不做的事（避免破坏可重入性）:

- 不引入全局可变状态
- 不把会话的 I/O 实现合并（`Send`/`Close` 的底层差异必须保留）
- 不改变 `ScalablePool` / `WorkerPool` 的 `submitMu` 与 `wg` 的既有顺序契约
- 不改动 `Gateway.Stop` 的四阶段顺序（StopAccept -> Drain -> CloseSessions -> StopAll）

---

## 7. 云服务器验证要求

按任务要求，服务端程序需在云服务器编译验证并记录数据。本轮验证的最小集:

1. 在云服务器拉取修复后的提交
2. `go build ./...` 与 `go vet ./...` 通过
3. `run_tests.go -mode unit` 与 `-mode integration` 通过
4. govulncheck 复跑，确认漏洞数归零
5. 记录：CPU 架构、核数、内存、内核版本、Go 版本、实测吞吐与延迟

数据将记入 `docs/reports/` 下的验证报告。

---

## 8. 执行顺序

```
[已完成] 全量审查 + 7 模式测试 + CI 失败归因
[第 1 步] A1-A4: 修复 lint 4 项 -> 提交 -> 验证 lint 绿
[第 2 步] A5-A7: 修复 go.mod 3 项 -> 提交 -> 验证 govulncheck 绿
[第 3 步] A9: 消除 D11 时序竞态 -> 提交 -> 验证 race 稳定绿
[第 4 步] A8: 全量 7 模式回归 -> 推送 -> 复核 CI 全绿
[第 5 步] 同步更新全部说明文档（仅 ASCII / 中文）
[第 6 步] 云服务器编译验证 + 记录数据
[第 7 步] S1-S7: 结构化去重（分阶段，每阶段独立提交）
```

第 1、2、3 步是放行其余工作的前提。

---

## 9. 修复后验证结果

本审查的全部修复项已逐项提交并验证。CI 状态从连续 8 次失败转为通过。

### 9.1 提交清单

| 提交 | 内容 | 对应缺陷 |
| --- | --- | --- |
| 2ab6c3d05 | fix(lint): 清除 5 处（实为 9 处）静态检查问题 | D1 |
| 6db5fd537 | style(scripts): gofmt workerpool_baseline.go | D3 |
| c62d4f0be | chore(deps): 升级工具链与两个模块，清除 govulncheck | D2 |
| 08c1e5c24 | test(shared): 使丢弃策略拒绝成为确定性断言 | D11 |
| 93391829d | docs: 本审查报告 | - |
| bf242c9f6 | docs: 文档重新同步（字符集/事实/CHANGELOG） | - |
| e0381168a | refactor(app): TLS 加载与缓存注册合并为一步 | D9 |
| 0082b4ead | docs(readme): 静态链接声明补上 CGO_ENABLED=0 前提 | D12 |
| 7960d97da | docs: 云端验证报告 + D12 入册 | - |
| 0ecf9563d | docs(changelog): 补入 D12 与云端验证结果 | - |

### 9.2 CI 验证

```
run 144  0ecf956  COMPLETED / SUCCESS   (3m 32s)  <- 最终提交
run 143  7960d97  CANCELLED                       <- 被 run 144 按 concurrency 策略取代
run 142  bf242c9  COMPLETED / SUCCESS
run 141  9339182  COMPLETED / SUCCESS
run 140  2483f59  FAILURE                         <- 修复前的 HEAD
run 139  2860a0f  FAILURE
run 138  629e6ca  FAILURE
```

修复前连续 8 次失败（run 133 之后全部为红）；修复后 run 141、142、144 连续三次
成功。工作流 badge 亦为 `CI - passing`。run 143 的 CANCELLED 是 `ci.yml` 中
`concurrency.cancel-in-progress: true` 的正常行为，不是失败。

### 9.3 各修复项的验收证据

| 项 | 验收命令 | 修复前 | 修复后 |
| --- | --- | --- | --- |
| D1 | `golangci-lint run --max-same-issues=0 --max-issues-per-linter=0 ./...` | 9 issues, rc=1 | 0 issues, rc=0 |
| D2 | `govulncheck ./...` | 11 个可达漏洞, rc=3 | 0 个, rc=0 |
| D3 | `gofmt -l .` | 1 个文件 | 空 |
| D11 | `run_tests.go -mode race` | 间歇失败 | 连续通过 |
| D9 | `go test ./internal/app/` | - | 通过（含热重载用例） |
| D12 | 云端 `file` 产物 | 文档与实测不符 | 文档已与实测一致 |

### 9.4 云端验证

见 `docs/reports/CLOUD-VALIDATION-V10-261016.md`：Linux x86_64 + go1.26.9 上
9 步全部通过，覆盖率 79.6%，6 组压力用例零失败，govulncheck 0 漏洞。

### 9.5 未实施项说明

D4、D5、D6、D7、D8 五项结构性重复（约 400 行）**未在本轮实施**，原因已在第 5
节说明：它们集中在 8 个传输层的并发关键路径（会话访问器、`touch`、`Close` 骨架、
register/OnAccept/回滚），一次性大批量改写与"尽可能保持可重入性"的要求直接冲突。
第 5 节给出的 S1-S7 分阶段方案中，S1（新增 shared 会话基类）与 S2（先迁移 grpcweb）
是首批候选，每阶段以 unit + integration + race 三模式全绿为放行条件。

D10（生产代码 0% 覆盖：websocket 的 RemoteAddr/LocalAddr、scripts/rateclient 的
`readFull`）同样未补测。其中 `readFull` 是本轮之前刚加入的帧读取修正逻辑，
优先级最高。

---

## 10. 双 8C/16G 主机复验与追加修复

第 9 节的验证跑在 120.76.44.233 上，那是一台 2 vCPU / 1607 MB 的共享 k8s 节点，
验证期间负载一度达到 80，且 `govulncheck` 会因内存不足被 OOM 杀死。为得到可信的
吞吐数据并在另一组环境上交叉验证，本轮改用两台 8 vCPU / 15.3GB 的专用主机。

- 审查基线: main @ 66e81e13c
- 复验主机: srv2 47.120.44.128, srv3 8.130.190.141（均 Ubuntu 26.04, go1.26.9, gcc 15.2.0）
- 报告: `docs/reports/CLOUD-VALIDATION-SRV23-261016.md`

**同一 commit 在两台机器上结果不一致**，由此追加两项缺陷。这印证了第 7 节把
云端验证列为例行要求的价值：单机验证会把环境差异误判成代码缺陷或稳定性。

### D13 (P1) 部署测试的可用性判定不成立

`tests/deploy/deploy_test.go` 的 `TestDeployToolRenderingWhenAvailable` 用
`exec.LookPath` 判定工具可用性，问的是"PATH 上有没有叫 docker 的文件"，而不是
"`docker compose` 能不能跑"。srv3 的 `docker.io` 包不带 compose 插件（`docker
compose` 报 `unknown command`，退出码 125），判定通过而子命令失败。

一个根因同时拉红三项：`integration`、`deploy` 与 `race` 都包含 `./tests/deploy`。
三份日志中失败的断言都只有 `deploy_test.go:149` 一处，这是判定"三项同源"的依据。

已修复（提交 `d2e7b00c1`），可用性改为探测实际能力（`toolRuns`）。`kubectl` 与
`helm` 同属"二进制存在不等于能力可用"的类别，一并处理。双向验证：srv2（有
compose v5.5.0）修复后**无跳过日志**，说明渲染真的执行；srv3 正确跳过；srv3 的
integration / deploy / race 由 1/1/1 变为 0/0/0。

### D14 (P2) 后台清扫器的默认间隔分支从未被覆盖，且覆盖率不可复现

覆盖率在同一 commit 上不可复现（srv2 79.7% / srv3 79.6%）。逐函数比对
`go tool cover -func` 定位到 3 个函数，其中只有 `Memory.StartSweeper` 的
`interval <= 0` 分支是系统性缺口 -- 树内调用方无一传非正值，该函数恒停在 88.9%。

已补测（提交 `66e81e13c`），88.9% -> 100.0%，两台主机各 5 次独立单跑均为 100.0%。

修完后重跑，差异仍在，但受影响的函数**完全换了一批**：第一轮是
`cache.StartSweeper` / `grpcweb.pingLoop` / `grpcweb.closeWebSocketSession`，
第二轮是 `plugin/cluster.go Start` / `tcp/session.go readLoop` /
`websocket/server.go pingLoop`，两轮集合无交集。

因此这不是若干固定薄弱点，而是同一类竞态在不同函数上随机显现：所有受影响函数的
共同属性是定时器驱动的 goroutine 循环，其退出与测试收尾之间没有同步点。

**记为测量边界，不作为缺陷追踪**：覆盖率可复现区间为 79.7% - 79.8%，期望偏差
+/-0.1 个百分点。无法靠补测消除 -- 要做到确定性只能改生产 API 暴露 goroutine
完成信号，或加入概率性 sleep，后者本项目在 D11（池丢包测试）上已明确否决过一次。
对 CI 无实际影响（门限 70%）。README 与 README.zh-CN.md 的覆盖率声明已改为区间
并附原因。

### 10.1 与本轮相关的状态更新

| 项 | 第 9 节 | 第 10 节 |
| --- | --- | --- |
| 验证主机 | 1 台 2 vCPU / 1.6GB（共享 k8s 节点） | 2 台 8 vCPU / 15.3GB（专用） |
| 步骤结果 | 9 步通过 | 两台各 10 步通过 |
| 覆盖率 | 79.6%（声明为与本地一致） | 79.7% - 79.8%（区间，见 D14） |
| govulncheck | 需 `GOMEMLIMIT` 规避 OOM | 默认参数一次通过，0 漏洞 |
| 产物可复现 | 未核对 | 两台 sha256 相同（`61d52e8b...`） |
| CI | run 141/142/144/145 SUCCESS | 追加 run 146/147 SUCCESS |
| 未实施项 | D4-D8, D10 | 结论不变，见 9.5 与 10.2 |

### 10.2 未实施项（第 10 节结束时）

D4、D5、D6、D7、D8（结构性重复）、D10（0% 覆盖的生产函数）状态与第 9.5 节相同，
本轮未推进。D13、D14 为本轮新增并已闭环。D14 的残余部分（定时器 goroutine 退出
竞态）明确不实施，理由见上。

