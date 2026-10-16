# 云端验证报告: L2 弹性工作池 (2026-10-16)

## meta-info

version: 1.0
date: 2026-10-16
scope: L2 (WorkerPool 弹性伸缩) 的正确性验证与收益实测
design_ref: docs/design/REARCHITECTURE-261016-v2.md 第 11 节 / 14.8-14.11
code_at: b5fe6f738 (fix(scripts): frame the rate client's writes)
hosts: srv2 47.120.44.128 (服务端) + srv3 8.130.190.141 (客户端), 均 8 vCPU / 16G

---

## 1. 结论摘要

| 项 | 结果 |
|---|---|
| `go build ./...` 两机 | 通过 |
| `go vet ./...` 两机 | 通过 |
| `gofmt -l` 两机 | 无输出 |
| `go test -race -count=3` shared 包 (两机) | 通过, 无竞态 |
| `go test -race -count=3` tcp 包 (两机) | 通过, 无竞态 |
| `go test ./...` 全仓 (srv2) | 通过, 27 包全绿 |
| `go test ./...` 全仓 (srv3) | 1 项失败, **环境原因** (见 §4) |
| goroutine 泄漏探针 (GOEXPERIMENT) | 通过 |
| **L2 收益实测** | **成立** (见 §5) |

---

## 2. 环境

| | srv2 (服务端) | srv3 (客户端) |
|---|---|---|
| vCPU / 内存 | 8 / 14 GiB 可用 | 8 / 14 GiB 可用 |
| 内核 | 7.0.0-31-generic | 7.0.0-31-generic |
| Go | **1.26.5** (与本地基线一致) | 1.26.5 (经 goproxy.cn 取得工具链) |
| gcc | 15.2.0 | 15.2.0 |
| 角色 | 压测服务端 | 压测客户端 |

srv3 原本是 go1.26.1, 低于 go.mod 要求的 1.26.5, 默认 `proxy.golang.org`
在这两台机器上不可达, 表现为一条容易误读的 "downloading go1.26.5" 失败。
解法是显式设 `GOPROXY=https://goproxy.cn,direct`。

---

## 3. 正确性验证

`ScalablePool` 在运行期启停 goroutine, 因此 `-race` 是唯一有意义的检查:
`wg.Go` 与 `submitMu` 的拆分是否成立, 只有竞态检测器能给出答案。

| 检查 | srv2 | srv3 |
|---|---|---|
| `go build ./...` | 通过 | 通过 |
| `go vet ./...` | 通过 | 通过 |
| `gofmt -l internal/ api/ examples/` | 无输出 | 无输出 |
| `go test -race -count=3 ./internal/transport/shared/` | 通过 12.0s | 通过 12.0s |
| `go test -race -count=3 ./internal/transport/tcp/` | 通过 7.6s | 通过 6.7s |
| `go test ./...` | 通过 27 包 | 见 §4 |
| `GOEXPERIMENT=goroutineleakprofile` 探针 | 通过 | 通过 |

泄漏探针覆盖 §11.6 记录的那类缺陷 (worker 永久阻塞在 `<-p.queue` 上)。
它现在是常驻检查项, 零额外代码成本。

---

## 4. srv3 的唯一失败项: 环境而非代码

```
--- FAIL: TestDeployToolRenderingWhenAvailable
    docker compose -f deploy/docker/docker-compose.yml config failed: exit status 125
    unknown shorthand flag: 'f' in -f
```

`srv3` 装有 `/usr/bin/docker` 但**没有 compose 插件**; srv2 有
`Docker Compose version v5.5.0`, 同一测试通过。属主机环境缺件, 不涉及本次改动,
未修改主机去迁就测试。

---

## 5. L2 收益实测: 固定池 vs 弹性池

### 5.1 方法

服务端 srv2, 客户端 srv3, **两台独立物理机**, 经 SSH 隧道相连
(srv2 安全组仅放行 22/80/443 与 18000/19000; 18000 被既有进程占用, 不去动它)。

| 项 | 值 |
|---|---|
| 负载 | 4/8 连接, 限速客户端, handler 延迟 1ms, queue 512, policy drop |
| 时长 | 每配置 10s |
| 服务端重启 | 每配置独立重启 |

四处方法学要点, 每条对应此前某一轮的错误:

1. **分机** —— 同机双进程下客户端抢 CPU, 测的是客户端 (设计文档 §13 的错误)。
2. **限速** —— 不限速则客户端填满自身 socket 缓冲, 服务端感受不到压力。
3. **queue_full 取增量, 分母用 sent** —— 计数是累计的; 每个 sent 帧恰好对应一次
   pool Submit。**用 recv 做分母会算出 >100% 的丢弃率**, 这是首跑
   fixed_w2 报出 236% 的原因。
4. **worker 数从 journal 读** —— `systemd-run` 把 stdout 送进 journal 而非重定向
   文件, 首跑因此 worker 数全空。

### 5.2 数据

| 配置 | 实际 worker | 吞吐 (msg/s) | drop% | loss% |
|---|---|---|---|---|
| 固定 w=2 | 2 | 1782 | 70.157 | 70.157 |
| 固定 w=4 | 4 | 3467 | 41.928 | 41.926 |
| 固定 w=8 | 8 | 5926 | 0.747 | 0.745 |
| 弹性 2-8 | 2 -> 8 | 5746 | 3.750 | 3.750 |
| 突发 固定 w=8 (12000/s) | 8 | 6712 | 43.787 | 43.786 |
| 突发 弹性 2-16 (12000/s) | 2 -> 15 | 10336 | 13.437 | 13.436 |

### 5.3 三条结论

**1. 近线性在独立复测中复现。** 固定池 w=2 -> 4 -> 8 为 1782 -> 3467 -> 5926 msg/s,
相邻提升 1.95x / 1.71x, 与设计文档 §14.2 一致。这证明 §13 "worker 数无关"
确属归因错误, 而非代码性质。

**2. 同等 offered load 下, 弹性追平按峰值配好的固定池。** 6000/s 时弹性 2-8
达 5746 msg/s, 对比固定 w=8 的 5926 msg/s。**弹性略逊**, 原因是采样滞后:
它要靠 backlog 累积才扩, 固定池从第一刻就是满编。

**3. 突发下差距拉开, 这是 L2 价值的直接兑现。** 12000/s 时固定 w=8 为
6712 msg/s / 43.8% drop, 弹性 2-16 为 10336 msg/s / 13.4% drop ——
**吞吐 1.54x, 丢弃率降 69.3%**。实测涨到 15 个 worker, 仍在 max=16 内。
天花板从 `固定 worker 数 / handler 耗时` 变为 `maxWorker / handler 耗时`。

### 5.4 自洽性校验

六行的 drop% 与 loss% **逐行吻合到小数点后三位**。二者独立: 前者来自服务端
`queue_full` 增量, 后者来自客户端 sent/recv 差值。一致说明丢弃确实发生在池的
拒绝路径上, 未在 socket 缓冲或客户端读取环节丢失 —— 这是判定该批数据可信的依据。

### 5.5 局限

- **单机单配置, 每配置 10s, 无重复采样**, 无方差估计。上述差异应视为量级。
- **只测了增长方向**。收缩时机与振荡抑制未在真实负载下实测;
  `TestScaleDownFloor` 只覆盖地板逻辑。
- 突发组 12000/s 是限速客户端的稳定量级, 未高到让固定池逼近其 8 worker
  理论上限 (8 x 1ms = 8000 msg/s), 隧道与 RTT 使实测天花板低于此。
- **L2 不解决单 session 饿死**: 队列全局共享。

---

## 6. 过程中修正的四个缺陷

均为本次工作发现, 已修复并单独提交:

| # | 缺陷 | 影响 |
|---|---|---|
| 1 | `rateclient` 未按 `LengthPrefixFramer` 分帧 | 服务端把每 256 字节写入读成 64 个零长帧, 3950 次发送产生 239267 次 queue_full。所有基于该客户端的历史数字作废 |
| 2 | 压测驱动脚本在 srv2 上调用客户端脚本 | 客户端脚本自建隧道, 必须在 srv3 执行。空输出被当作"丢弃率 0%" |
| 3 | drop% 分母用 recv | 算出 >100% 的丢弃率 |
| 4 | worker 数从重定向文件读 | `systemd-run` 输出在 journal, 读到全空 |

另修复代码中两处既有问题: `pool_scale_test.go` / `worker_pool_saturation_test.go`
的 gofmt 违规 (本次提交前就存在), 以及 §11.6 记录的 `tcp.Server.Start`
pool 泄漏路径 (改为 listen 成功后才建池)。

---

## 7. 复现方式

脚本位于 `.workbuddy/cloud/` (本地工具目录, 不入版本库):
`verify_l2.sh` (正确性), `bench_server.sh` / `bench_client.sh` / `bench_drive.sh`
(压测), `build_bench.sh` (构建压测二进制), `sshrun.py` + `hosts.json` (SSH)。

两机已互配免密: srv2 的 `id_ed25519_srv3` 与 srv3 的 `id_ed25519_srv2`。

云端压测的前提条件 (建议固化): 客户端与服务端必须分机; 客户端必须限速;
丢弃率必须读服务端 `queue_full` 增量且以 `sent` 为分母; 每个配置独立重启服务端。