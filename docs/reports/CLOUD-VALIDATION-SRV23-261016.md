# 云端编译验证报告 - 双 8C/16G 主机批次

## meta-info

- 批次: V10 Review - Multi-Host Validation Batch
- 本地基线: main @ 659417c84 (含 D13/D14 两项修复)
- 验证主机: srv2 47.120.44.128 (iZf8zj5ccfontaezzi4kfrZ)
            srv3 8.130.190.141 (iZ0jlcgedkf3ybet8e1quuZ)
- 验证时间: 2026-10-16 17:42 - 18:16 (UTC+8), 含三轮: bd5d41a8f (首轮) /
            d2e7b00c1 (D13 后) / 66e81e13c (D14 后) / 659417c84 (最终确认)
- 源码交付方式: git clone (gitee), 每步前校验 HEAD 与本地一致
- 远端路径: /root/shark-socket
- 远端日志: /root/reports/cloud-validate-261016-180554.log (D14 后)
            /root/reports/cloud-validate-261016-181408.log (最终)

## 1. 主机环境

| 项 | srv2 | srv3 |
| --- | --- | --- |
| 实例 ID | iZf8zj5ccfontaezzi4kfrZ | iZ0jlcgedkf3ybet8e1quuZ |
| OS | Ubuntu 26.04.1 LTS | Ubuntu 26.04.1 LTS |
| 内核 | 7.0.0-31-generic | 7.0.0-31-generic |
| 架构 | x86_64 | x86_64 |
| vCPU | 8 (Intel Xeon Platinum) | 8 (Intel Xeon Platinum) |
| 内存 | 15320 MB | 15320 MB |
| 磁盘 | / 40G, 已用 6.2G (17%) | / 40G, 已用 5.7G (16%) |
| Go (供给前) | go1.26.5 | go1.26.1 |
| Go (供给后) | go1.26.9 linux/amd64 | go1.26.9 linux/amd64 |
| gcc | 15.2.0 (Ubuntu 15.2.0-16ubuntu1) | 15.2.0 (Ubuntu 15.2.0-16ubuntu1) |
| docker | 29.7.2 (community) | 29.1.3 (Ubuntu 包) |
| docker compose | v5.5.0 插件可用 | **无 compose 插件** |
| GOPROXY | https://goproxy.cn,direct | https://goproxy.cn,direct |
| GOSUMDB | off | off |
| 验证期间 1 分钟负载 | 1.77 - 2.63 | 2.10 - 2.60 |

两台均为专用实例, 未承载 k8s 或容器编排组件, 空闲内存 13.8 GB 以上。这与上批次的
120.76.44.233 有本质区别: 后者是 2 vCPU / 1607 MB 的共享 k8s 节点, 验证期间负载曾达
80。本批次的吞吐数字因此具有参考价值, 而上批次明确不能作为性能基准。

**两台的唯一环境差异是 docker compose 插件的有无**, 这一差异直接导致了第 3 节的缺陷。

### 1.1 工具链供给

两台的预装 Go 版本都低于 go.mod 的 `go 1.26.9` 指令, 且 `proxy.golang.org` 不可达
(实测 HTTP 000)。供给方式为从 `mirrors.aliyun.com` 显式安装:

```
https://mirrors.aliyun.com/golang/go1.26.9.linux-amd64.tar.gz
66935201 字节
sha256 42d158b4d8f7b61ac0a830567c940a86098fb7aac52e467a5ebec03ef5cc2f8d
```

两台下载到的 sha256 完全相同, 工具链可复现。选择显式安装而非依赖 `GOTOOLCHAIN` 自动
下载, 是因为自动下载路径在上一批次已被记录为首次解压时存在竞争; 两台主机同属阿里云,
走区域内镜像也更快。

`GOSUMDB=off` 是必要的: `sum.golang.google.cn` 从这些主机不可达。模块哈希仍以
`go.sum` 校验, 关闭的只是外部校验和数据库查询。

## 2. 逐步结果

最终 HEAD `66e81e13c` 在两台主机上均 **10 步全绿**。

| 步骤 | 命令 | srv2 | srv3 |
| --- | --- | --- | --- |
| [0/9] go env | `go env` | 0 | 0 |
| [1/9] 依赖下载 | `go mod download` | 0 (0s) | 0 (0s) |
| [2/9] 编译 | `go build ./...` | 0 (2s) | 0 (3s) |
| [3/9] 静态检查 | `run_tests.go -mode vet` | 0 (0s) | 0 (0s) |
| [4/9] 单元测试 | `run_tests.go -mode unit` | 0 (6s) | 0 (6s) |
| [5/9] 集成测试 | `run_tests.go -mode integration` | 0 (65s) | 0 (62s) |
| [6/9] 部署验证 | `run_tests.go -mode deploy` | 0 (9s) | 0 (8s) |
| [7/9] 覆盖率 | `run_tests.go -mode cover` | 0 (6s) | 0 (7s) |
| [8/9] 竞态检测 | `CGO_ENABLED=1 run_tests.go -mode race` | 0 (65s) | 0 (63s) |
| [9/9] 漏洞扫描 | `govulncheck ./...` | 0 (14s) | 0 (11s) |
| 追加 | `go build -o reports/shark-socket ./cmd/shark-socket` | 0 (1s) | 0 (1s) |

- 单元测试: 21 个 package 全 `ok`, 0 失败
- 竞态检测: 27 个 package 全 `ok`, 0 失败, 0 条 `DATA RACE`
- 集成测试: 15 个用例全 `--- PASS`, 0 失败 (含 `TestExamplesCompile` 的 11 个 example)
- 覆盖率: srv2 **79.8%**, srv3 **79.7%** (门限 70%), 差异原因见第 4 节
- 漏洞扫描: 两台均 `No vulnerabilities found`, 0 个可达漏洞

最终 HEAD `659417c84` (仅文档改动, 代码与 `66e81e13c` 逐字节相同) 又跑了一遍作确认:
两台仍为 10 步全绿, 覆盖率两台均为 **79.7%**, 产物 sha256 两台相同。该轮的逐步数据
与上表一致 (integration 64s / deploy 7-9s / cover 6-7s / race 63-65s),
日志见 meta-info 末行。

`govulncheck` 在 16GB 内存的机器上以默认参数一次通过, 不需要上批次在 1.6GB 机器上
使用的 `GOGC=20 GOMAXPROCS=1 GOMEMLIMIT=600MiB` 规避手段。

## 3. 缺陷 D13: 部署测试的可用性判定不成立 (1 个根因, 3 个失败步骤)

### 3.1 现象

首次运行 (HEAD `bd5d41a8f`) 时, 同一 commit 在两台机器上结果不一致:

| 步骤 | srv2 | srv3 |
| --- | --- | --- |
| integration | rc=0 | **rc=1** (65s) |
| deploy | rc=0 | **rc=1** (7s) |
| race | rc=0 | **rc=1** (92s) |
| 其余 7 步 | rc=0 | rc=0 |

三项失败看起来像三个独立问题, 实际只有一个根因 -- 三项日志里失败的断言都只有一处:

```
srv2 deploy 日志: 无 FAIL
srv3 deploy 日志: deploy_test.go:149: docker compose -f deploy/docker/docker-compose.yml
                                   config failed: exit status 125
srv3 race   日志: deploy_test.go:149: (同上)
```

`integration` 模式与 `race` 模式都包含 `./tests/deploy`, 因此同一个断言把三个步骤一起
拉红。定位方式是先确认三份日志的失败断言集合相同, 而不是逐项当成独立缺陷排查。

### 3.2 根因

```go
// 修改前
if _, err := exec.LookPath("docker"); err == nil {
    out := runCommand(t, root, "docker", "compose", "-f", "deploy/docker/docker-compose.yml", "config")
    ...
}
```

`exec.LookPath` 回答的是"PATH 上有没有一个叫 docker 的文件", 而测试需要的是
"`docker compose` 能不能跑"。Ubuntu 的 `docker.io` 包把 docker 与 compose 插件分开
打包, srv3 上 docker 在而插件不在, 于是:

```
srv3:  docker --version          -> Docker version 29.1.3, build 29.1.3-0ubuntu4.1
srv3:  docker compose version    -> docker: unknown command: docker compose
srv3:  docker compose ... config -> exit status 125
srv2:  docker compose version    -> Docker Compose version v5.5.0
srv2:  docker compose ... config -> 正常输出, rc=0
```

可用性判定通过, 子命令却失败, 测试因此报失败而不是跳过。这是"缺少能力"被误判为
"代码有缺陷"。

### 3.3 修复

改为探测而非推断, `kubectl` / `helm` 同样处理:

```go
// toolRuns reports whether a CLI is installed AND the given invocation works.
//
// exec.LookPath on its own is an unsound availability gate for a subcommand.
// Ubuntu packages docker.io without the compose plugin, so `docker` resolves
// while `docker compose` exits 125 with "unknown command"; a host that merely
// packaged its tools differently was then reported as a test failure instead of
// the skip it is. Probing answers the question the test actually asks -- can
// this host run the command -- rather than the weaker question of whether a
// file with that name exists on PATH.
func toolRuns(name string, args ...string) bool {
	if _, err := exec.LookPath(name); err != nil {
		return false
	}
	return exec.Command(name, args...).Run() == nil
}
```

判定改为 `toolRuns("docker", "compose", "version")` / `toolRuns("helm", "version")` /
`toolRuns("kubectl", "version", "--client")`。

修复没有削弱测试强度: 用 `compose version` 探测不需要 docker daemon, 所以确实具备
该能力的机器仍会完整渲染清单, 真正的清单错误仍然会被抓到。只有能力缺失的机器才跳过。

### 3.4 双向验证

| 主机 | 修复前 | 修复后 | 关键观察 |
| --- | --- | --- | --- |
| srv2 (compose v5.5.0) | PASS | PASS | **无跳过日志**, 说明 `docker compose config` 真的执行并通过 |
| srv3 (无 compose 插件) | FAIL (exit 125) | PASS | 跳过并打日志 `docker compose not available` |
| srv3 integration / deploy / race | 1 / 1 / 1 | **0 / 0 / 0** | 三项同时转绿 |
| 本地 build + tests/... + gofmt | - | 0 / 通过 / 干净 | - |

只验证"失败的那台不再失败"是不够的, 因为把判定改成恒假也能达到同样效果。srv2 上
没有跳过日志这一点, 是修复后正例仍然生效的证据。

提交 `d2e7b00c1`, CI run 146 SUCCESS (2m 7s)。

## 4. 缺陷 D14: 覆盖率不可复现, 并暴露出一个未覆盖分支

### 4.1 现象

同一 commit 的两台主机报出不同覆盖率: srv2 **79.7%**, srv3 **79.6%**。覆盖率是
验证记录的一部分, 不可复现意味着报告里"与本地一致"之类的结论不成立。

### 4.2 定位方法

不用整体百分比, 而是逐函数比对 `go tool cover -func` 的输出 (835 行):

```
$ cd /root/shark-socket && go tool cover -func=logs/coverage.out | sort
$ diff covfunc_srv2.txt covfunc_srv3.txt
150c150
< internal/infra/cache/cache.go:48:       StartSweeper      88.9%
> internal/infra/cache/cache.go:48:       StartSweeper      77.8%
523c523
< internal/transport/grpcweb/server.go:332:  pingLoop        100.0%
> internal/transport/grpcweb/server.go:332:  pingLoop         75.0%
525c525
< internal/transport/grpcweb/server.go:348:  closeWebSocketSession  100.0%
> internal/transport/grpcweb/server.go:348:  closeWebSocketSession   85.7%
835c835
< total:                                    (statements)      79.7%
> total:                                    (statements)      79.6%
```

三个函数受影响。其中两个是"某次运行覆盖到、另一次没覆盖到"的竞态, 而
`cache.StartSweeper` 是**系统性缺口**: 它的上限就是 88.9%, 与调度无关。

### 4.3 系统性缺口: StartSweeper 的默认间隔分支

```go
func (m *Memory) StartSweeper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {           // 该分支从未被任何测试执行
		interval = time.Minute
	}
	...
}
```

树内所有调用方都传显式正值, 时序驱动的清扫测试传的是 10ms, 因此该分支恒不可达。

这个分支值得单独立测, 原因是它的失效方式: 它是调用方传 0 与 `time.NewTicker(0)`
之间的唯一防护, 而 `NewTicker` 对非正周期会 panic; panic 发生在清扫器 goroutine 上,
回归会直接中止整个测试二进制, 而不是让某个测试失败。补测后:

| 项 | 修复前 | 修复后 |
| --- | --- | --- |
| `StartSweeper` (srv2) | 88.9% (上限) | **100.0%** |
| `StartSweeper` (srv3) | 77.8% | **100.0%** |
| cache package 覆盖率 | < 100% | **100.0%** |
| 两台各 5 次独立单跑 | - | 10/10 均为 100.0% |

提交 `66e81e13c`, CI run 147 SUCCESS (1m 57s)。

### 4.4 残余差异的边界

修复后重跑, 总覆盖率变为 srv2 79.8% / srv3 79.7%, 差异仍然存在, 但**受影响的函数
完全换了一批**:

| 轮次 | 提交 | srv2 | srv3 | 受影响函数 |
| --- | --- | --- | --- | --- |
| 第一轮 | `d2e7b00c1` | 79.7% | 79.6% | `cache.StartSweeper`, `grpcweb.pingLoop`, `grpcweb.closeWebSocketSession` |
| 第二轮 | `66e81e13c` | 79.8% | 79.7% | `plugin/cluster.go Start`, `tcp/session.go readLoop`, `websocket/server.go pingLoop` |
| 第三轮 | `659417c84` | 79.7% | 79.7% | (本轮两台一致) |

第一轮与第二轮的集合没有交集 (`cache.StartSweeper` 已修至 100%, `grpcweb.*` 在第二轮
两台都是 100%)。这一点是结论的关键: 受影响函数逐次变化, 说明它不是若干固定薄弱点,
而是同一类竞态在不同函数上随机显现。所有受影响函数的共同属性是**定时器驱动的
goroutine 循环 (`Start` / `readLoop` / `pingLoop`), 其退出与测试收尾相互交错** --
测试结束与 goroutine 观察到 `ctx.Done()` 并执行 `return` / `defer Stop()` 之间没有
同步点。

第三轮两台读出一致值, 但这不构成反例: 该轮恰好所有相关 goroutine 都在收尾前完成。
三次观测合起来的区间是 79.6% - 79.8%。

因此:

- 覆盖率在 **79.6% - 79.8%** 区间内可复现, 但**不是逐位可复现**; 期望偏差
  +/-0.1 个百分点
- 无法通过补测消除: 这些 goroutine 的完成不可观测, 要做到确定性只能改生产 API 暴露
  完成信号, 或加入概率性 sleep -- 后者本项目已经在池的丢包测试上明确否决过一次
- 对 CI 无实际影响: 门限是 70%, 当前值高出近 10 个百分点

结论: 记为**测量边界**而非缺陷。今后报告覆盖率应写成区间, 不应写"与本地一致"。

## 5. 跨主机压测: 固定池与弹性池

### 5.1 方法

- 服务端 srv2, 客户端 srv3, 两台物理机分离。同机同时跑客户端会偷走服务端的 CPU,
  测出的"吞吐"描述的是客户端。
- 客户端限速, 让压力落在服务端的队列上。
- 丢包取自服务端自己的 `tcp_task_queue_full_total`, 不用客户端侧丢失推断。
- 丢包率是两个快照之间的**增量**。计数器是累积的, 单次快照会算出大于 100% 的值。
- 每组配置重启一次服务端。指标是惰性注册的, 事件首次触发前计数器不存在, 复用服务端
  会让基线失真。
- 服务端以独立 systemd unit 启动, 按监听端口定位进程。`pkill -f` 会匹配到驱动脚本
  自己的命令行并杀掉运行压测的 SSH 会话。

二进制从被测 commit `d2e7b00c1` 重建, 两端的 `rateclient` sha256 一致
(`331ba58269af9f0cea47e8d93e69ba5d2b92d6ebc0346734c30caa1c59cd5ba2`)。
handler 延迟 1ms, 队列 512, 丢包策略。

### 5.2 度量修正

首轮数据的校准基线多数读到 `-1`。`-1` 是"计数器尚未注册"的哨兵值, 不是计数 -1;
把它代入减法会让每个增量恰好多 1 个事件。驱动脚本原本只在 `delta < 0` 时兜底, 没有
处理哨兵。

这一偏差由独立的交叉校验暴露: 脚本同时计算 `(sent - recv) / sent`, 该量与队列满
计数完全无关, 因此可作为第二个估计。下表全部取自**同一轮**运行, 逐行可直接核验:

| 用例 | 基线 | final | 原始 delta | 修正 delta | drop (原始) | drop (修正) | loss (独立) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| fixed_w4 | -1 | 25230 | 25231 | 25230 | 42.263% | 42.261% | 42.261% |
| fixed_w8 | -1 | 55 | 56 | 55 | 0.094% | 0.092% | 0.092% |
| elastic_2_8 | -1 | 2400 | 2401 | 2400 | 4.022% | 4.020% | 4.020% |
| burst_fixed_w8 | -1 | 53007 | 53008 | 53007 | 44.395% | 44.394% | 44.394% |
| burst_elastic_2_16 | -1 | 13537 | 13538 | 13537 | 11.338% | 11.338% | 11.338% |

修正后, `drop` 与独立的 `loss` 在每一行上**逐位相等**; 修正前有 4/5 行相差
0.001 - 0.002 个百分点。两个独立算法一致, 是增量取值正确的证据, 也是修正方向正确
的证据 -- 若偏差方向相反, 修正只会让两者差得更远。

脚本已修正为 `if (b < 0) b = 0` 后再相减, 并重跑一轮。**下节采用修正后那一轮的
数据**; 上表的作用是说明修正的依据, 其数值不进入结论。

### 5.3 结果

| 用例 | worker (实际 / 边界) | 投递条数 | 吞吐 msg/s | 丢包 |
| --- | --- | --- | --- | --- |
| fixed_w2 | 2 / (2,2) | 17738 | 1774 | **70.288%** |
| fixed_w4 | 4 / (4,4) | 34611 | 3461 | **42.025%** |
| fixed_w8 | 8 / (8,8) | 59423 | 5942 | **0.464%** |
| elastic_2_8 | 8 / (2,8) | 57275 | 5728 | 4.062% |
| burst_fixed_w8 | 8 / (8,8) | 67007 | 6701 | **43.880%** |
| burst_elastic_2_16 | 14 / (2,16) | 103222 | 10322 | **13.549%** |

前四组与后两组分别是 6000/s 与 12000/s 的供给, handler 均为 1ms。

**弹性池的价值**: 12000/s 供给下, 固定 8 worker 投递 67007 条、丢包 43.88%;
弹性池 (2-16) 投递 103222 条、丢包 13.55%。同等负载下多投递 **54.0%**, 把固定上限
造成的拒绝挽回了约七成。

**单 worker 服务速率**: `fixed_w2/w4/w8` 分别为 887 / 865 / 838 msg/s/worker, 即
每条约 1.13 - 1.19 ms, 相对名义 1ms 的 handler 有 0.13 - 0.19 ms 的框架开销。

**一处如实记录的不利结论**: 6000/s 供给下弹性池丢包 4.062%, 高于固定 8 worker 的
0.464%。原因是弹性池从 4 worker 起步, 扩容爬坡落在 10 秒测量窗口内, 窗口前段仍在
拒绝。这是固定宽度与弹性宽度在**短窗口**下的固有取舍, 不是实现缺陷, 但不应被
"弹性池更好"的总结掩盖。

**轮间波动**: 弹性突发用例在两轮中分别落在 9 与 14 个 worker、投递 105863 与 103222
条 (相差 2.5%)。worker 数差 5 而吞吐接近, 说明该点已接近供给上限 (12000/s)
而非池容量上限。弹性池的吞吐在短窗口下对扩容 tick 落点敏感, 重复测量应报区间。

## 6. 集成测试内含压力用例

两台主机分别运行, 均为 `fail=0`。

| 用例 | srv2 吞吐 | srv2 P50/P90/P99 | srv3 吞吐 | srv3 P50/P90/P99 |
| --- | --- | --- | --- | --- |
| TCPConnections | 126925.7 msg/s | 346us / 678us / 1.18ms | 111099.9 msg/s | 397us / 778us / 1.32ms |
| TCPBurst | 15535.6 msg/s | 8.33ms / 12.31ms / 14.14ms | 16349.1 msg/s | 6.98ms / 13.02ms / 14.48ms |
| TCPReconnect | 30609.2 msg/s | 1.21ms / 2.36ms / 4.14ms | 25461.0 msg/s | 1.48ms / 2.83ms / 4.86ms |
| UDPConnections | 98616.9 msg/s | 474us / 597us / 718us | 99926.2 msg/s | 487us / 559us / 658us |
| WSChurn | 16140.1 msg/s | 310us / 2.40ms / 4.93ms | 12925.7 msg/s | 353us / 2.91ms / 6.06ms |
| HTTPRequests | 69088.0 msg/s | 512us / 1.59ms / 3.20ms | 59558.5 msg/s | 590us / 1.98ms / 3.58ms |

与上批次 2 vCPU / 1.6GB 共享节点相比 (TCPConnections 24587.6, HTTPRequests
12378.7, WSChurn 2433.1 msg/s), 8 核专用主机的提升符合预期, 主要来自调度余量而非
代码差异 -- 两次的 commit 不同, 但被测行为未变, 因此这组对比只能读作**环境量级**。

两台之间的差异 (TCPConnections 126925.7 vs 111099.9, 差 12.5%) 属于同构机器间的
正常波动, 与本批次的 docker compose 差异无关, 不宜据此推断哪台更快。

## 7. 产物与构建可复现性

| 项 | srv2 | srv3 |
| --- | --- | --- |
| 路径 | reports/shark-socket | reports/shark-socket |
| 大小 | 13954176 字节 | 13954176 字节 |
| sha256 (`66e81e13c`) | 61d52e8b4bce2b1c7e22be73145d2199ab8811354e656a2e3e3b3ffd22bded24 | **同左** |
| sha256 (`659417c84`) | 29e267ea7eb180f461d7508706d782d7f72ab3533211819dc320f60b71c68abf | **同左** |
| vcs.revision | 659417c844482fcbe6c605fd0f430c645f23398d | **同左** |
| vcs.modified | false | false |
| 链接 | ELF 64-bit LSB executable, dynamically linked (libc) | **同左** |

两台独立编译出的产物字节级相同, 构建可复现。

两个 HEAD 之间的 sha256 不同是预期的, 而不是复现性被破坏: Go 自 1.18 起默认把 VCS
信息嵌入二进制, 提交号变化必然改变产物。**同一 commit 跨主机一致**才是有意义的判据,
这一点在两轮上都成立。`vcs.modified=false` 同时说明被测工作树是干净的, 没有未提交
改动混入测量。

链接方式为动态, 与 Dockerfile 的 `CGO_ENABLED=0` 静态构建不同。这不是新发现
(上一批次已记 D12 并在两版 README 补充前提), 本次仅作对照: 两台都装有 gcc, 因此
裸跑 `go build` 时 `CGO_ENABLED` 默认为 1, cgo 版 DNS 解析器被启用, 产物依赖
`libc.so.6`。`-race` 同样需要 gcc 与 `CGO_ENABLED=1`, 本批次在两台均正常。

## 8. CI 状态

| run | commit | 结论 | 耗时 |
| --- | --- | --- | --- |
| 145 | `bd5d41a8f` | SUCCESS | 1m 59s |
| 146 | `d2e7b00c1` (D13) | SUCCESS | 2m 7s |
| 147 | `66e81e13c` (D14) | SUCCESS | 1m 57s |
| 148 | `659417c84` (文档同步) | SUCCESS | 1m 51s |
| 149 | `168cba3b9` (覆盖率区间修正) | SUCCESS | 2m 5s |

工作流徽章为 `CI - passing`。两个远端 (github origin / gitee) 与本地均同步。
本报告自身的提交也会触发一次 CI; 纯文档提交不改变被测代码, 因此上式只登记到
最后一个影响记录的提交。

## 9. 验证脚本自身的修正

本轮在驱动脚本 `.workbuddy/cloud/` 上修了三处, 都不属于被测代码但影响数据可信度:

1. **退出码取自流水线最后一环**。`govulncheck ./... 2>&1 | tail -12` 之后的 `$?`
   取的是 `tail` 的退出码。上一批次因此出过假绿 (安装失败、二进制不存在, 却报
   `rc=0`)。改为 `${PIPESTATUS[0]}`, 并额外检查二进制是否可执行。
2. **Git Bash 改写远端路径**。MSYS 会把 `/root/x.sh` 这类参数改写成
   `C:/Program Files/Git/root/x.sh`, 所有 SFTP 上传都失败并报 `FileNotFoundError`。
   封装脚本中设 `MSYS_NO_PATHCONV=1` 与 `MSYS2_ARG_CONV_EXCL='*'`。
3. **本地变量在多层解析中被提前展开**。`\$PATH` 经多层解析后仍在本地被展开成
   Windows 的 PATH 并送进远端命令, 导致语法错误。改为"本地写脚本、上传、远端执行"
   的模式, 远程命令中不再出现 shell 变量。

另外建议: 只要命令经过多层引号与变量, 就不要在远程命令里内联 `$VAR`; 用绝对路径
或上传脚本更可靠。

## 10. 结论

1. V10 批次改动在**两台 8C/16G Ubuntu 26.04 主机上均 10 步全绿**, 编译 / 静态检查 /
   单元 / 集成 / 部署 / 覆盖率 / 竞态 / 漏洞扫描全部退出码 0, 27 个 package 竞态检测
   零告警, 0 个可达漏洞。最终 HEAD `659417c84` 上重复确认一次, 结论不变。
2. 发现并修复 2 项缺陷: D13 (部署测试可用性判定不成立, 一个根因造成 3 个步骤失败,
   提交 `d2e7b00c1`) 与 D14 (`StartSweeper` 默认间隔分支从未被覆盖, 88.9% -> 100%,
   提交 `66e81e13c`)。两项均先在云端复现、定位到具体行、修复后再双向验证。
3. 覆盖率**不是逐位可复现**: 可复现区间为 79.6% - 79.8%。残余偏差来自定时器驱动
   goroutine 的退出与测试收尾之间的竞态, 受影响函数逐轮变化, 无法通过补测消除。
   今后应报区间, 不应声明与本地完全一致。对 CI 无影响 (门限 70%)。
4. 弹性池在 12000/s 供给下比固定 8 worker 多投递 54.0% (103222 vs 67007 条),
   丢包 13.55% vs 43.88%。同时如实记录: 短窗口下弹性池因扩容爬坡, 在 6000/s 供给时
   丢包反而高于固定 8 worker (4.062% vs 0.464%)。
5. 同一 commit 下两台独立编译的产物 sha256 相同, 构建可复现; 二进制内嵌
   `vcs.revision`, 因此跨 HEAD 的 sha256 变化是预期的。
6. 三处脚本缺陷已修正并记录, 其中 `${PIPESTATUS[0]}` 一项可避免"假绿"。
7. CI run 145 - 149 全部 SUCCESS。
