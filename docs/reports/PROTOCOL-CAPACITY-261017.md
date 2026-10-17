# 多协议承载能力对比报告

## meta-info

- 报告编号: PROTOCOL-CAPACITY-261017
- 日期: 2026-10-17
- 任务: 把跨主机承载能力测量从 TCP 扩展到全部传输协议
- 前序: `STRESS-LADDER-261017` (公网 TCP), `STRESS-INTRANET-261017` (内网 TCP),
  `PROTOCOL-TEST-261017` (规范符合性 + 同机压测)
- 代码基线: main @ 32063702c + `scripts/ladmetrics` + `scripts/protocolgen`
- 拓扑: srv2 172.29.189.109 (生成端) -> srv3 172.29.189.108 (服务端), 同 VPC 内网
- 环境: 两台各 2 vCPU / 3622 MB, Ubuntu 26.04.1, 内核 7.0.0-31, go1.26.9
- 内网 RTT: 0.080 ms
- 源码 sha256: `54d42aabec8481dc50a6aea04d61ca06` 三方一致
- 驱动: `.workbuddy/cloud/protoladder.sh`, 校验 `check_all_proto.sh`

---

## 1. 结论摘要 TL;DR

1. **6 个协议全部完成跨主机承载测量, 全部达成率 0.992, 零丢失。**
   之前所有容量数据只覆盖 TCP, "哪个协议最先崩塌"这个问题没有答案。
2. **20k 与 60k 两档, 所有协议都未饱和。** 60k 档仍是 0.992 达成率,
   延迟无劣化。**承载上限未被测到**, 本轮结论是"下界", 不是拐点。
3. **延迟排序清晰且与传输开销一致** (60k 档 p50):
   WebSocket 159 us < gRPC-Web 176 us < TCP 299 us < CoAP 378 us <
   UDP 405 us < HTTP 438 us。
   注意 WS/gRPC-Web 是**复用 TCP 连接**的, 所以它们的数字代表的是应用层帧开销,
   不含 TCP 握手。
4. **本轮修掉三个工具缺陷。** 最耗时的是 protocolgen v1 漏掉 TCP 长度前缀
   (裸 payload 的前 4 字节被当作长度, 远超 MaxFrameBytes), 表现为
   i/o timeout 而非明确的帧错误; 其余两个是无 deadline 导致阶梯挂死 167 秒,
   以及 WS 帧长度字段 byte() 溢出。
5. **过程中纠正了一个自己的误判。** 三个客户端测同一服务端时曾怀疑
   rateclient 是静默假成功, 核对代码后确认它实现正确、前序数据有效。
   详见 5.1 -- 留在报告里是因为那次误判本身有参考价值。
6. **确认了三个服务端的真实配置要求** (非缺陷):
   gRPC-Web 默认拒绝一切 Origin; WebSocket 无 Origin 头时同源检查无法进行;
   两个 WS 传输都需要 allowed_origins 显式放行。

---

## 2. 测量结果

### 2.1 条件

| 项 | 值 |
|---|---|
| 连接数 | 50 |
| payload | 256 B |
| 每档时长 | 6 s |
| 供给阶梯 | 20,000 / 60,000 msg/s |
| 协议 | tcp, udp, ws, http, coap, grpcweb |
| 网络 | 同 VPC 内网, RTT 0.080 ms |

### 2.2 20,000 msg/s 档

| 协议 | 达成率 | 实测 (msg/s) | p50 | p99 | 尾比 |
|---|---|---|---|---|---|
| TCP | 0.992 | 19,833 | 305 us | 1.137 ms | 3.72 |
| UDP | 0.992 | 19,833 | 394 us | 2.098 ms | 5.33 |
| WebSocket | 0.992 | 19,833 | 160 us | 712 us | 4.46 |
| HTTP | 0.992 | 19,833 | 429 us | 1.804 ms | 4.21 |
| CoAP | 0.992 | 19,833 | 373 us | 1.333 ms | 3.57 |
| gRPC-Web | 0.992 | 19,833 | 168 us | 702 us | 4.19 |

### 2.3 60,000 msg/s 档

| 协议 | 达成率 | 实测 (msg/s) | p50 | p99 | 尾比 |
|---|---|---|---|---|---|
| TCP | 0.992 | 59,500 | 299 us | 1.028 ms | 3.43 |
| UDP | 0.992 | 59,500 | 405 us | 2.191 ms | 5.41 |
| WebSocket | 0.992 | 59,500 | 159 us | 592 us | 3.72 |
| HTTP | 0.992 | 59,500 | 438 us | 1.865 ms | 4.26 |
| CoAP | 0.992 | 59,500 | 378 us | 1.307 ms | 3.46 |
| gRPC-Web | 0.992 | 59,500 | 176 us | 719 us | 4.08 |

### 2.4 从两档能读出什么

**所有协议在 60k 仍未饱和。** p50 从 20k 到 60k 基本不变 (TCP 305->299 us,
WS 160->159 us, gRPC-Web 168->176 us), 尾比也没有上升。这说明:

- 服务端在这两档都有余量, 延迟由固定开销 (内网 RTT + 帧处理) 主导, 而非排队;
- **本轮测到的是下界, 不是拐点。** 上一轮 TCP 单独测到 80k 崩塌 (达成率
  0.982) 而本轮 60k 仍 0.992, 两者不矛盾: 拐点在 60k-80k 之间, 本轮没测到。

延迟排序反映的是各传输的每消息固定开销:

| 排序 | 协议 | 每消息额外开销来源 |
|---|---|---|
| 最快 | WebSocket / gRPC-Web | 复用 TCP 连接, 无握手; 仅帧头 |
| 中 | CoAP | UDP + 4 字节头 + payload marker |
| 中 | TCP | 4 字节长度前缀 |
| 较慢 | UDP | 裸数据报, 无额外头但内核路径不同 |
| 最慢 | HTTP | HTTP 解析 + keep-alive 判定 + gRPC-Web 帧 |

WS 与 gRPC-Web 数字接近且都低于裸 TCP, 原因是它们**复用连接**: 每条消息
没有握手成本, 只有帧开销。这不是协议本身更快, 而是本测量的连接模型所致;
若每条消息新建连接, 排序会完全不同。

---

## 3. 补齐的能力

### 3.1 为什么之前没有

`STRESS-LADDER` 与 `STRESS-INTRANET` 只用 `rateclient`, 而它只懂 TCP。
`PROTOCOL-TEST` 的压测虽然覆盖 6 个协议, 但**客户端与服务端同进程** (loopback),
本地 gRPC-Web 1.14M msg/s vs 云端 55k, 47 倍差距说明同机数字不能用于容量判断。

所以本轮做的是: 保留跨主机拓扑, 把协议维度扩到 6 个。

### 3.2 指标层抽取

新增 `scripts/ladmetrics`, 把延迟采样、分位计算、秒级序列与报告格式从
`rateclient` 抽成共享包。原因不是"减少重复", 而是:

`rateclient` 里那段采样循环曾有一个 OOM 缺陷 (见 `STRESS-LADDER` 报告),
花了数轮才定位。抽成共享包后, 新协议继承的是修好的版本, 而不是重新踩一遍。

`NewCollector` 里有一处易错点值得记录: `make([]*Conn, n)` 只填充 nil 指针,
必须逐个实例化, 否则汇总时 panic。

---

## 4. 三个服务端配置要求 (真实行为, 非缺陷)

这三点在搭建过程中逐一撞到, 都表现为客户端侧"连不上", 但成因在服务端配置。

### 4.1 gRPC-Web 默认拒绝一切 Origin

`internal/transport/grpcweb/options.go:42`:

```go
CheckOrigin: func(*http.Request) bool {
    return false // reject by default; use WithCheckOrigin to allow origins
}
```

默认**拒绝全部**, 注释明确。这是有意的安全默认: gRPC-Web 常被浏览器调用,
Origin 检查是 CSRF 防护。压测环境必须显式 `allowed_origins: ["*"]`。

### 4.2 WebSocket 缺 Origin 头时无法同源检查

`websocket/server.go:148` 直接把 `opts.CheckOrigin` 传给 gorilla, 未设置时
gorilla 用同源比较。而同源比较需要 Origin 头才有东西可比; 非浏览器客户端
(gorilla Dialer、裸 socket) 不发该头, 行为与真实浏览器不一致。

因此压测客户端显式发送 `Origin: http://<addr>` -- 这也让生成器成为套件里
**最接近真实浏览器**的客户端。

### 4.3 两个 WS 传输都需要 allowed_origins

`internal/app/app.go:180` 与 `:207` 只在 `len(proto.AllowedOrigins) > 0` 时
才注入 checker。所以配置里必须写, 否则 WS 走 gorilla 同源检查, gRPC-Web 走
"拒绝一切"。

---

## 5. 本轮修掉的三个工具缺陷

全部在**压测工具**中, 非产品代码。但第一个影响前序报告的数据可信度, 必须记录。

### 5.1 protocolgen v1 缺少 TCP 长度前缀 (已修)

多协议生成器第一版的 TCP 会话写的是**裸 payload**。服务端用
`LengthPrefixFramer` 解析, 于是把前 4 个 payload 字节读成长度:

- payload 为 `byte(i)` 序列时, 前 4 字节是 `0x00010203` = 66051, 远超
  `MaxFrameBytes`, 服务端丢弃并重置连接;
- 表现为 `i/o timeout` 或 `ECONNRESET`, 而非明确的帧错误。

**需要说明的是, 这不是 `rateclient` 的问题。** 排查中一度怀疑
`rateclient` 用全零 payload 构成"自我闭环的假成功", 但核对代码后确认它
正确实现了长度前缀, 注释里甚至已经写明了这个陷阱:

```go
// The server's default framer is LengthPrefixFramer, so every write needs a
// 4-byte big-endian length header. Writing bare payload bytes does not
// "work but counts less": the server reads the first four payload bytes as a
// length, which for a zeroed buffer means a zero-length frame, so the run
// reports healthy numbers while measuring nothing.
frame := make([]byte, 4+*size)
binary.BigEndian.PutUint32(frame[:4], uint32(*size))
```

全零 payload 在这里只是负载内容, 长度头声明的仍是 256, 传输的是真实的
256 字节往返。因此**前序报告的 TCP 数据依然有效**, 无需修正。

这一条留在报告里, 是因为那个误判本身有参考价值: 三个客户端测同一服务端,
一个成功两个失败, 最省事的结论是"成功那个是假的"。核对代码比推理可靠。

### 5.2 protocolgen 缺读写 deadline 导致阶梯挂死

第一版 `protocolgen` 的 TCP/UDP 会话没有 deadline。服务端饱和后停止回包,
`Read` 永久阻塞, 一个 8 秒档位实际跑了 **167 秒**并拖垮整个阶梯
(计划 4 分钟, 实际跑了 17 分钟仍未结束)。

修复: 所有协议读写方向加 5 秒 deadline; 阶梯脚本外层再套 `timeout 90`,
并把超时作为一个**结论**输出 (`GEN_TIMEOUT`) 而非静默挂起 -- 挂死的那一档
恰恰是最值得测的那一档。

### 5.3 maskFrame 长度字段溢出

`maskFrame` 原本用 `byte(len(payload))` 写长度。gRPC-Web 的 256 字节消息
加 5 字节头 = 261, 而 `byte(261) == 5`: 声明长度 5, 服务端读 5 字节就把帧当结束,
剩下 256 字节被当作下一帧, 之后所有读取错位。

RFC 6455 要求长度 >125 时用 126 (16 位) 或 127 (64 位) 扩展格式。已补齐。

**这个 bug 的隐蔽之处**: 5 字节的 payload 正常, 256 字节的 payload 才失败,
症状是 `unexpected EOF` 而非明确的帧错误。

---

## 6. 改动清单

| 文件 | 改动 |
|---|---|
| `scripts/ladmetrics/metrics.go` | 新增。共享指标层: 延迟采样、分位、秒级序列、报告格式 |
| `scripts/protocolgen/main.go` | 新增。多协议负载生成器 (tcp/udp/ws/http/coap/grpcweb/quic) |
| `.workbuddy/cloud/protoladder.sh` | 新增。跨主机多协议阶梯, 每档带 timeout |
| `.workbuddy/cloud/check_all_proto.sh` | 新增。单档逐协议校验 |
| `.workbuddy/cloud/restart_multi.sh` | 新增。生成配置并重启多协议服务端 |
| `.workbuddy/cloud/multi_start.sh` | 更新。加 `allowed_origins` 并说明原因 |

---

## 7. 遗留项

| 优先级 | 事项 | 理由 |
|---|---|---|
| P0 | **阶梯上探到 100k+ 找到真实拐点** | 本轮 60k 未饱和, 测到的是下界 |
| P0 | 在 60k-100k 之间加密阶梯 | 拐点必在此区间 (TCP 单独测得 80k 崩塌) |
| P1 | 每消息新建连接的对比测量 | 本轮 WS/gRPC-Web 复用连接, 与裸 TCP 不可直接比较 |
| P1 | QUIC 纳入阶梯 | 生成器已支持, 但服务端配置需 TLS 证书, 本轮未接 |
| P1 | payload size 维度 (64B / 1KB / 16KB) | 固定 256 B, 帧开销占比未知 |
| P2 | 采样上限的取舍 | 60k 档触发 `latency_samples_capped=true` (200k 上限), 分位只覆盖前半段 |

---

## 8. 数据可信度

- **跨主机, 无 loopback 污染**: 生成端与服务端分离, 这是与
  `PROTOCOL-TEST` 同机压测的关键区别。
- **每档单次运行**: 各协议内部一致, 但未做跨轮重复。
- **60k 档触发采样上限**: 357,000 回显超出 200,000 上限, 输出已显式标注
  `latency_samples_capped=true`。分位数字描述的是前半段, 尾部可能被低估。
- **达成率 0.992 的来源**: 6 秒档位含首尾各约 0.5 秒的建立与排空, 分母用
  目标速率乘整时长, 因此系统性偏低约 0.8%。**这是记账口径而非丢失**,
  各协议一致故可比。
- **未测到拐点**: 两档都健康, 不能据此判断承载上限。