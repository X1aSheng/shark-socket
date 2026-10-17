# 跨主机压测驱动

两个主机的压测流程: 服务端跑在目标机, 负载生成器跑在另一台, 两者跑同一份
源码。

## 为什么必须分机

同机测试会让生成器与服务端抢 CPU, 并让生成器自己的 socket 缓冲区参与测量,
于是数字描述的是生成器而不是服务端。实测差距很大: gRPC-Web 同机
1,144,686 msg/s 对跨机 54,645 msg/s, 47 倍, 全部来自 loopback
(见 `docs/reports/PROTOCOL-CAPACITY-261017.md`)。

## 文件

| 脚本 | 跑在哪 | 用途 |
|---|---|---|
| `sync_build.sh` | 本地 | 把源码同步到远端并构建 |
| `serve_multi.sh` | 目标机 | 生成配置并启动 6 协议服务端 |
| `check_protocols.sh` | 生成端 | 逐协议校验生成器可用 |
| `ladder.sh` | 生成端 | 单协议供给阶梯, 定位拐点 |
| `protoladder.sh` | 生成端 | 全部协议阶梯, 横向对比 |
| `isolate_ceiling.sh` | 生成端 | 瓶颈定性 (带宽 / 每消息成本 / 连接数) |

端口分配 (`serve_multi.sh` 与 `ladder.sh` 必须一致):

| 协议 | 端口 | 路径 |
|---|---|---|
| tcp | 18000 | - |
| udp | 18002 | - |
| http | 18003 | `/grpc` |
| websocket | 18004 | `/ws` |
| coap | 18005 | - |
| grpc-web | 18006 | `/grpc/ws` |

## 用法

### 一次完整流程

```bash
# 1. 同步源码到两台机器并构建
REMOTE_HOST=<generator> REMOTE_USER=root bash sync_build.sh
ssh root@<target> 'cd /root/shark-socket && git fetch && git checkout -q FETCH_HEAD && \
  export PATH=/usr/local/go/bin:$PATH && go build -o /root/shark-multi ./cmd/shark-socket'

# 2. 目标机启动服务端
ssh root@<target> 'REPO=/root/shark-socket bash /root/shark-socket/scripts/loadtest/serve_multi.sh'

# 3. 生成端先校验 6 个协议可用 (重要: 生成器有 bug 时, 高档位的"饱和"是假的)
SERVER_HOST=<target> bash scripts/loadtest/check_protocols.sh

# 4. 阶梯压测
SERVER_HOST=<target> PROTOCOLS="tcp udp ws http coap grpcweb" \
  RATES="20000 60000 120000" bash scripts/loadtest/protoladder.sh

# 5. 找到拐点后定性瓶颈
SERVER_HOST=<target> PROTOCOL=tcp bash scripts/loadtest/isolate_ceiling.sh
```

### 只测 TCP

```bash
SERVER_HOST=<target> PROTOCOL=tcp RATES="80000 120000 145000 180000" \
  bash scripts/loadtest/ladder.sh
```

## 读结果

关键字段与它们的含义:

| 字段 | 含义 |
|---|---|
| `achieved_ratio` | 实测 / 供给。低于 1 有两种原因, 见下 |
| `errs` | 错误数。`errs=0` 且 `recv == sent` 说明零丢弃 |
| `p50` / `tail_ratio` | 中位延迟 / p99 与 p50 之比 |
| `latency_samples_capped` | 采样达上限, 分位只覆盖前半段 |
| `first_failure` | 生成器完全没跑起来时的原因 |

### 背压与崩塌的区别

`achieved_ratio < 1` 不等于服务端在丢消息。生成器是同步请求-响应, 供给超过
服务端能力时会被响应速度自然拖慢, 这时:

| | 背压 (健康) | 崩塌 |
|---|---|---|
| `errs` | 0 | 大量超时或连接重置 |
| `recv` vs `sent` | 相等 | 明显小于 |
| `p50` | 稳定 | 显著抬升 |
| `tail_ratio` | 稳定 | 急剧上升 |
| 秒级序列 | 平稳 | 断崖后新稳态 |

只有崩塌形态才说明服务端在丢消息。背压形态下 `achieved` 就是服务端能力。

### 拐点在哪里

`achieved_ratio >= 0.99` 的最后一个档位即拐点。低于 0.98 起持续下降说明已过拐点。

## 前提

- 目标机已装 Go (脚本用 `/usr/local/go/bin`)
- 两个 WS 传输需要 `allowed_origins`, `serve_multi.sh` 已配 `"*"`
- 生成器与服务端必须同一 commit, 数字才有意义
- **两台机器都必须空闲。** `serve_multi.sh` 与 `ladder.sh` 都会检查目标机 /
  生成端是否有竞争负载并在发现时告警。

### 为什么强调空闲

这不是形式主义。实测遇到过: 目标机上跑着一个无关的浸泡测试 (占 126% CPU,
两核机器), 同样配置的 TCP 吞吐从 119k msg/s 掉到 65k msg/s, 而**生成器与
服务端双方的日志里没有任何错误**。

最自然的解读是"服务端退化了", 但真正的原因是 CPU 被抢占。压测数字只在
独占机器上才有意义, 所以两个脚本都会在产生负载前检查:

```
HOST LOAD CHECK
  load average: 2.12 1.90 1.73 (cores=2)
  WARNING: processes using at least half a core each:
    126% loadgen
```

出现该告警时, 应当先停掉或改期那些工作, 再重新测量。

### SSH 说明

`sync_build.sh` 通过 ssh 同步源码, 需要免密或 agent 转发。其余脚本在**目标机上
直接运行**, 不依赖 ssh:

```bash
# 目标机
bash scripts/loadtest/serve_multi.sh

# 生成端
bash scripts/loadtest/check_protocols.sh
```

## 相关报告

- `docs/reports/PROTOCOL-CAPACITY-261017.md` -- 跨主机多协议承载对比
- `docs/reports/TCP-SATURATION-261017.md` -- TCP 拐点 142k 与瓶颈定性
- `docs/reports/STRESS-INTRANET-261017.md` -- 内网重测与弹性池对比
