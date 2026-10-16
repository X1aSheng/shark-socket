# 云端编译验证报告 - V10 CI 恢复批次

## meta-info

- 批次: V10 Review - CI Restore Batch
- 本地基线: main @ 0082b4ead (含 D1/D2/D3/D9/D11/D12 六项修复)
- 验证主机: 120.76.44.233 (阿里云 ECS, iZwz93b9fvc8lttmuewj0cZ)
- 验证时间: 2026-10-16 17:03:12 - 17:10:47 主流程, govulncheck 补充至 17:30
- 源码交付方式: git archive (core.autocrlf=false), 613601 字节
- 远端路径: /opt/shark-v10/src
- 远端日志: /opt/shark-v10/reports/cloud-validate-261016-170312.log

## 1. 主机环境

| 项 | 值 |
| --- | --- |
| OS | Ubuntu 26.04 LTS |
| 内核 | 7.0.0-15-generic |
| 架构 | x86_64 |
| vCPU | 2 (Intel Xeon Platinum) |
| 内存 | 1607 MB |
| 磁盘 | / 40G, 已用 14G (37%) |
| Go | go1.26.9 linux/amd64 |
| gcc | 15.2.0 (Ubuntu 15.2.0-16ubuntu1) |
| GOPROXY | https://goproxy.cn,direct |
| 其他 | 该机同时承载 dockerd / containerd / k8s (metrics-server, local-path-provisioner) 与阿里云安全 agent |

Go 版本与 go.mod 的 `go 1.26.9` 指令一致, 这是本批次升级后的目标版本。

## 2. 逐步结果

全部 9 步通过。

| 步骤 | 命令 | 退出码 | 耗时 |
| --- | --- | --- | --- |
| [0/9] go env | `go env` | 0 | - |
| [1/9] 依赖下载 | `go mod download` | 0 | 1s |
| [2/9] 编译 | `go build ./...` | 0 | 59s |
| [3/9] 静态检查 | `run_tests.go -mode vet` | 0 | 23s |
| [4/9] 单元测试 | `run_tests.go -mode unit -timeout 10m` | 0 | 37s |
| [5/9] 集成测试 | `run_tests.go -mode integration -timeout 15m` | 0 | 81s |
| [6/9] 部署验证 | `run_tests.go -mode deploy` | 0 | 15s |
| [7/9] 覆盖率 | `run_tests.go -mode cover -timeout 15m` | 0 | 44s |
| [8/9] 竞态检测 | `CGO_ENABLED=1 run_tests.go -mode race -timeout 20m` | 0 | 178s |
| [9/9] 二进制产物 | `go build -o bin/shark-socket ./cmd/shark-socket` | 0 | 2s |
| 追加 | `govulncheck ./...` (受限内存, 见 4.2) | 0 | - |

覆盖率: **79.6%** (与本地一致, 门限 70%)。

单元测试 21 个 package 全部 `ok`; 集成测试 15 个用例全部 `--- PASS`; 用例含
`TestExamplesCompile` 的 11 个 example 逐个编译通过, 以及
`TestGitHubActionsWorkflowSemantics` (校验 CI 工作流本身)。

补充的 govulncheck 输出:

```
=== Symbol Results ===

No vulnerabilities found.

Your code is affected by 0 vulnerabilities.
This scan also found 0 vulnerabilities in packages you import and 2
vulnerabilities in modules you require, but your code doesn't appear to call
these vulnerabilities.
```

与本地扫描结论一致 (0 个可达漏洞, 2 个仅存在于模块图中但未被调用)。

## 3. 压力测试吞吐对比

集成测试内含 6 组压力用例, 均为 `fail=0`。同序对比如下:

| 用例 | 云端 120.76.44.233 (2 vCPU / 1.6GB) | 本地 (Windows, 对照) | 比值 |
| --- | --- | --- | --- |
| TCPConnections | 24587.6 msg/s (245882 条) | 141140.9 msg/s | 0.17 |
| TCPBurst | 5301.4 msg/s (500 条) | 3931.4 msg/s | 1.35 |
| TCPReconnect | 5605.2 msg/s | 7681.0 msg/s | 0.73 |
| UDPConnections | 70243.3 msg/s (702458 条) | 85498.3 msg/s | 0.82 |
| WSChurn | 2433.1 msg/s (24349 条) | 5425.7 msg/s | 0.45 |
| HTTPRequests | 12378.7 msg/s (123804 条) | 103776.9 msg/s | 0.12 |

**解读**: 这组数字不能读作"云端更慢", 因为两台机器的可比性有限。

1. 云端是 2 vCPU 且与该机自身的 k8s 组件 (dockerd, metrics-server,
   local-path-provisioner, 阿里云安全 agent) 共享 CPU。验证期间该机 1 分钟
   负载一度达到 80, 15 分钟均值 18, 远超 2 核的容量。
2. 本地是空闲的多核机器。TCPConnections / HTTPRequests 这类连接数驱动的
   用例对调度延迟最敏感, 因此差距最大 (0.17 / 0.12); 而 TCPBurst 只发 500 条,
   受调度抖动影响小, 反而略高。
3. 结论: 本次云端验证的**目的是编译与测试通过性**, 吞吐数字在此仅作为
   "在 2 核受限环境下仍能完整跑完全部用例且零失败"的证据, 不作为性能基准。
   性能基准应使用空闲的同构机器, 且需固定 GOMAXPROCS。

## 4. 本次发现的代码/文档问题

### 4.1 静态链接声明缺少前提 (已修复)

在云端执行 `go build` 后 `file` 的产物显示**动态链接**, 与 README 开篇
"statically linked" 的表述不符:

```
默认构建      : ELF 64-bit LSB executable, x86-64, dynamically linked,
                interpreter /lib64/ld-linux-x86-64.so.2   (依赖 libc.so.6)
CGO_ENABLED=0 : ELF 64-bit LSB executable, x86-64, statically linked
```

原因: 仓库无任何代码需要 cgo, 但在装有 C 工具链的主机上 `CGO_ENABLED` 默认为 1,
cgo 版 DNS 解析器被启用, 产物因而动态链接。Dockerfile 已显式 `CGO_ENABLED=0`,
所以镜像没问题; 但裸跑 `go build` 的用户会拿到一个在 Alpine/musl 上无法运行的
二进制。已在 README 与 README.zh-CN.md 补充前提、构建命令与两种产物对照
(记为 D12, 提交 0082b4ead)。

### 4.2 验证脚本的退出码缺陷 (本次脚本自身的问题, 已记录)

第一轮脚本中 govulncheck 一步写成:

```bash
go install ... 2>&1 | tail -2
"$(go env GOPATH)/bin/govulncheck" ./... 2>&1 | tail -12
echo "govuln rc=$?"
```

`$?` 取的是流水线最后一环 (`tail`) 的退出码, 而不是 govulncheck 的。首轮实际
情况是: `go install` 因 `sum.golang.google.cn` TLS 握手超时失败, 二进制根本没
装成, 脚本报出的 `govulncheck: No such file or directory` 被埋在 `tail` 之后,
而 `govuln rc=0` 是假象。此教训已记录: 用管道截断输出时必须以
`${PIPESTATUS[0]}` 取真实退出码。

修正为 `GOSUMDB=off` 后, govulncheck 在云端安装成功 (14916673 字节)。

### 4.3 云端 govulncheck 首次被 OOM 杀死 (环境限制, 已绕过)

以默认参数运行 `govulncheck ./...` 时进程被内核杀死, 输出仅 7 字节 `rc=137`
(128 + SIGKILL)。该机总内存 1607 MB, 且已常驻 dockerd / containerd /
metrics-server / 阿里云安全 agent; 扫描期间 1 分钟负载达到 80, 5 分钟均值 38。

以受限内存参数重跑即通过:

```
GOGC=20 GOMAXPROCS=1 GOMEMLIMIT=600MiB govulncheck ./...
-> No vulnerabilities found. Your code is affected by 0 vulnerabilities.
```

这是宿主机资源限制而非代码问题。若将来要在小内存机器上跑该扫描, 需要同时
压低 GC 目标、并行度与内存上限, 否则 OOM 会伪装成"扫描无输出"。

## 5. 结论

1. 本批次改动 (lint 修复 / 依赖升级 / gofmt / 时序竞态修复 / TLS 加载重构 /
   文档同步 / README 修正) 在 Linux x86_64 + go1.26.9 上**编译通过、静态检查
   通过、全部测试通过、漏洞扫描零命中**。
2. `go 1.26.9` 工具链在云端可用且与 go.mod 指令匹配, 验证了依赖升级的可移植性。
3. 覆盖率 79.6% 与本地一致, 说明改动未造成平台相关的覆盖差异。
4. 竞态检测在 2 核机器上 178s 跑完且无告警, 与本地结果一致。
5. govulncheck 在云端与本地均报告 0 个可达漏洞, 两处依赖图相同。
6. 两项环境层结论 (非代码缺陷): 该机内存不足以支撑默认参数的 govulncheck;
   该机是共享 k8s 节点, 其吞吐数字不能用作性能基准。
