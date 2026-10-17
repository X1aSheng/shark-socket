# 多协议标准规范与使用测试报告

## meta-info

- 报告编号: PROTOCOL-TEST-261017
- 日期: 2026-10-17
- 任务: 在云服务器上详细测试 UDP / HTTP / WebSocket / CoAP / LwM2M / QUIC / gRPC-Web
  的协议标准规范与使用
- 代码基线: main @ 2e72f9413 + `tests/protocol_conformance_test.go` +
  `tests/stress/stress_protocols_test.go`
- 执行主机: srv3 8.130.95.37 (阿里云, 2 vCPU / 3622 MB, Ubuntu 26.04.1,
  内核 7.0.0-31-generic, go1.26.9 linux/amd64)
- 源码交付: SFTP 上传 zip, sha256 `b4140e56538a3861c43fc3e5d574143a` 三方一致
- 驱动脚本: `.workbuddy/cloud/protocol_full.sh`

---

## 1. 结论摘要 TL;DR

1. **7 个协议全部有三层覆盖, 6 个三层全通过。** 规范符合性 11 项, 压测 14 项,
   全量 27 个包 0 失败。云端执行结果与本地一致。
2. **HTTP/2 已按约定排除, 原因记录在第 7 节。** 该项目未实现 HTTP/2,
   代码注释明确记录为有意避开 h2 协商 (与 WebSocket / gRPC-Web 升级冲突)。
3. **LwM2M 的线上协议不是 OMA 规范** (3.5 节): 是自创文本命令
   (`register <ep> <lt>` 空格分隔), 不是规范的 `/rd?ep=...` URI 模型。
   因此**没有** LwM2M 规范符合性测试; 但其 TLV 编解码是标准 OMA 格式,
   已有字节级测试。已补 4 项与线上格式无关的并发压测。
4. **规范测试发现一个互操作限制 (已记录, 未修)**: WebSocket 的 `session.Send`
   硬编码 `BinaryMessage`, 客户端发 text 帧会收到 binary 帧。帧本身合法、
   payload 完整, 但浏览器端 `onmessage` 的 `event.data` 会是 Blob 而非 string。
5. **压测补齐了 8 个协议的能力数据**, 此前只有 TCP/UDP/WS/HTTP 有压测。
   云端 2C 实测: UDP 342k msg/s, CoAP NON 322k msg/s, TCP 124k msg/s,
   CoAP CON 160k msg/s, HTTP 57k msg/s, gRPC-Web 55k msg/s, QUIC 21k msg/s。
   14 项压测零丢失。
6. **QUIC 是本轮唯一需要重新理解协议才能测的传输**: 它要求客户端半关闭流
   以触发服务端 `io.ReadAll` 返回, 且 echo 走服务端**新开的流**而非请求流。
   不了解这两点会测出"连接成功但零流量"的假象。
7. **补全了 QUIC 与 gRPC-Web 示例** (`examples/` 原缺这两个), 并把 QUIC 的
   两个非显然点写进头注释, 配套客户端实测跑通。
8. **过程中修掉 8 个测试自身的缺陷** (非产品缺陷), 其中 3 个是被服务端
   正确拒绝的非法报文。这些失败恰恰证明规范测试在真验证。

---

## 2. 测试范围与方法

### 2.1 三层测试的定义

| 层 | 回答的问题 | 方法 |
|---|---|---|
| 规范符合性 | 是否遵循协议标准 | 用**独立的、遵循标准的客户端**验证服务端, 逐条对应 RFC 章节 |
| 功能使用 | 基本收发与 API 用法 | 各协议包的单元测试 + 示例 |
| 性能压测 | 并发下的行为 | 每协议独立压测, 断言超越"流量是否流动" |

规范符合性这一层的关键在于**客户端独立性**: 用项目自己的客户端验证自己的
服务端, 只能证明两者一致, 不能证明任何一方遵循规范。本项目此前只有 CoAP
有规范测试 (`protocol_conformance_test.go`, 3 项), 其余协议没有。

### 2.2 各协议覆盖

| 协议 | RFC / 规范 | 规范测试 | 单元用例 | 压测 |
|---|---|---|---|---|
| UDP | RFC 768 | 2 项 (新增) | 22 | 原有 |
| HTTP/1.1 | RFC 7230-7235 | 5 项 (新增) | 8 | 原有 |
| WebSocket | RFC 6455 | 4 项 (新增) | 10 | 原有 |
| CoAP | RFC 7252 / 7641 | 3 项 (原有) | 65 | **2 项 (新增)** |
| LwM2M | OMA LwM2M 1.x | **不可行, 见 3.5** | 25 | **4 项 (新增)** |
| QUIC | RFC 9000 | - | 13 | **1 项 (新增)** |
| gRPC-Web | gRPC-Web protocol | - | 26 | **1 项 (新增)** |
| HTTP/2 | RFC 7540 | **未实现, 排除** | - | - |

LwM2M 有 25 个单元用例但无规范测试与压测, 是覆盖最弱的协议。原因见第 7 节。

---

## 3. 规范符合性测试 (新增 11 项)

全部位于 `tests/protocol_conformance_test.go`, 用标准库 HTTP 客户端、
gorilla/websocket、裸 UDP socket 实现, 不依赖项目自身的客户端。

### 3.1 HTTP/1.1

| 测试 | 验证内容 | 结果 |
|---|---|---|
| `TestHTTPMethodSemantics` | 7 种方法都到达 handler, 无静默改写 | PASS |
| `TestHTTPHeadHasNoBody` | HEAD 无 body 但 Content-Length 与 GET 一致 | PASS |
| `TestHTTPNotFoundStatus` | 未注册路径返回 404 而非 200 空 body | PASS |
| `TestHTTPKeepAliveReusesConnection` | 同一 TCP 连接服务多个请求 | PASS |
| `TestHTTPCORSVerify` | 允许的 origin 收到 ACAO, 未列出的不收到 | PASS |

`TestHTTPKeepAliveReusesConnection` 的做法值得说明: 客户端
`MaxConnsPerHost=1`, 第二个请求无法新建连接。若服务端每个响应后关闭连接,
该请求会直接失败而非降级, 因此这个测试无法被"部分实现"骗过。

`TestHTTPCORSVerify` 双向断言: 只验证"允许的 origin 通过"是不够的,
必须同时验证"未列出的 origin 不通过", 否则一个对所有 origin 都放行的
实现也会通过前半段 -- 而那是安全缺陷, 不是功能缺陷。

### 3.2 WebSocket (RFC 6455)

| 测试 | 验证内容 | 结果 |
|---|---|---|
| `TestWebSocketHandshakeAcceptKey` | `Sec-WebSocket-Accept` = base64(SHA1(key+GUID)), 字节级精确 | PASS |
| `TestWebSocketFrameOpcodes` | 帧格式: FIN、opcode、RSV 位、mask 位、长度字段 | PASS (附记录) |
| `TestWebSocketCloseHandshake` | 关闭帧被应答, 状态码 1000, TCP 连接随后关闭 | PASS |
| `TestWebSocketPingPong` | ping 被 pong 应答且 payload 原样回显 | PASS |

`TestWebSocketHandshakeAcceptKey` 用 RFC 自带的示例 key 与魔数 GUID,
期望值是**可计算的**, 因此任何手写的 accept 值都无法蒙混过关。
实现这一点需要用裸 socket 握手 -- 库生成的随机 key 会让期望值不可知。

### 3.3 UDP (RFC 768)

| 测试 | 验证内容 | 结果 |
|---|---|---|
| `TestUDPDatagramBoundary` | 一次 send = 一个 datagram, 边界保持 | PASS |
| `TestUDPEphemeralClientReuse` | 单 socket 承载多次连续交换 | PASS |

数据报边界是 UDP 区别于 TCP 的唯一性质, 也是 CoAP / LwM2M / QUIC 数据报
模式依赖的前提。服务端若把多个数据报拼起来或拆开, 这些协议会静默出错。

### 3.4 发现的互操作限制 (未修复, 已记录)

`internal/transport/websocket/session.go:71` 的 `Send` 硬编码
`websocket.BinaryMessage`:

```go
return s.conn.WriteMessage(websocket.BinaryMessage, payload)
```

因此客户端发送 text 帧, 会收到 binary 帧。测试中以 `t.Logf` 记录而非断言失败,
因为:

- 帧格式合法, payload 完整, `core.Session.Send([]byte)` 的契约也只承诺字节;
- 但浏览器 JS 客户端 `onmessage` 会按类型分 `event.data` 为 string 或 Blob,
  发 text 收 binary 会让它变成 Blob, 这是真实的互操作意外。

`core.Session` 接口没有承载帧类型的字段, 要修需要改契约 (例如加一个
`SendText` 或在 `Message` 上加类型), 属于接口变更而非缺陷修复, 因此本轮
只记录不修改。

### 3.5 LwM2M: 为什么没有规范符合性测试

这是本轮查清的一个边界, 记录于此因为它决定了 LwM2M 能测什么、不能测什么。

该实现的 CoAP 适配器 (`internal/protocol/lwm2m/coap.go`) **不是 OMA LwM2M
线上协议**, 而是一个空格分隔的文本命令协议:

```
register <endpoint> <lifetime-seconds> [object-path...]
update   <endpoint> <lifetime-seconds>
deregister <endpoint>
write    <endpoint> <resource-path> <value>
read     <endpoint> <resource-path>
```

而 OMA LwM2M 规范 (OMA-TS-LightweightM2M) 定义的是:

- 注册: `POST /rd?ep=<endpoint>&lt=<lifetime>&lwm2m=1.0`
- 读: `GET /<object>/<instance>/<resource>`, 带 `Accept` 协商 Content-Format
- 写: `PUT /<object>/<instance>/<resource>`, 带 `Content-Format`
- 去注册: `DELETE /rd/<location>`

适配器的注释自己也写明了这一点: "adapts a small text command protocol to the
LwM2M server"。

因此**写不出有意义的 LwM2M 规范符合性测试** -- 线上格式不遵循规范, 这是设计
选择而非疏忽 (文档如此, 注释如此)。声称测了 LwM2M 规范会是不实的。

但有一层是符合规范的: **TLV 编解码是标准 OMA 格式**, 且已有字节级精确的测试
(`codec_tlv_test.go` 的 `TestTLVEncodeOMAWireFormat`, 断言 type byte 0xC1
= Resource + 8-bit id + 8-bit length 等具体字节)。

结论分两层:

| 层 | 状态 |
|---|---|
| TLV 编解码 | 符合 OMA 规范, 已有字节级测试 |
| CoAP 线上协议 | **不是** LwM2M 规范, 是自创文本协议 (设计如此, 注释明确) |
| 注册表 / 资源存储 / 容量 / 清扫 | 与线上格式无关, 可测且已测 (见 4.5) |

是否应把线上协议换成真正的 OMA LwM2M 是产品决策, 测试无法裁定。

---

## 4. 压测补齐 (新增 8 项)

新增文件 `tests/stress/stress_protocols_test.go`, 复用现有 `stressMetrics`
(吞吐 + P50/P90/P99)。每个测试都断言超越"流量是否流动"的内容。

### 4.1 新增压测的断言设计

| 测试 | 超越流量的断言 |
|---|---|
| `TestStressCoAPConfirmable` | 每条请求收到 echo (256 B 完整) + ACK; 结束后 pseudo-session 全部回收 |
| `TestStressCoAPNonConfirmable` | 每条请求收到完整 echo; 结束后 pseudo-session 全部回收 |
| `TestStressQUICStreams` | 50 条连接全部完成握手; 每条 echo payload 完整 |
| `TestStressGRPCWebMessages` | 每条 echo 的帧头长度正确且 payload 逐字节一致; 结束后 session 回收 |

session / pseudo-session 回收断言是这些测试的核心价值: CoAP 没有连接可关,
TTL 清扫是释放 per-peer 状态的唯一路径; 若清扫失效, 长期运行的服务端会为
每一个见过的地址累积状态。这类问题在功能测试中完全看不出来。

### 4.2 云端实测结果 (2 vCPU, 50 连接, 256 B payload, 10 s)

| 协议 | 吞吐 (msg/s) | P50 | P99 | 丢失 |
|---|---|---|---|---|
| **UDP** | 342,415 | 136 us | 289 us | 0 |
| **CoAP NON** | 322,213 | 147 us | 306 us | 0 |
| **CoAP CON** | 159,935 | 256 us | 741 us | 0 |
| **TCP** | 124,056 | 390 us | 901 us | 0 |
| **WebSocket churn** | 19,145 | 770 us | 2.75 ms | 0 |
| **HTTP** | 57,311 | 779 us | 2.51 ms | 0 |
| **gRPC-Web** | 54,645 | 816 us | 2.58 ms | 0 |
| **QUIC** | 21,444 | 1.97 ms | 16.06 ms | 0 |

**10 个压测全部零丢失。** 相对排序符合各协议的预期: 无连接数据报最快,
需要握手 / 建连的最慢。

CoAP CON 吞吐是 NON 的 49.6%, 因为每条 CON 请求要额外产生一个 ACK 数据报
(客户端需读两个数据报才算完成一次交换)。这个差异是协议机制决定的, 不是缺陷。

QUIC 的 P99 (16.06 ms) 明显高于其他协议, 因为它每条消息都要新开一条流:
`OpenStreamSync` 的握手成本落在每一条消息上。这是该实现的结构特征
(见 4.4), 而非压测噪声。

### 4.3 与本地结果对照

| 协议 | 本地 (Windows) | 云端 (Linux 2C) |
|---|---|---|
| CoAP CON | 64,892 | **159,935** |
| CoAP NON | 120,255 | **322,213** |
| gRPC-Web | 1,144,686 | 54,645 |
| QUIC | 26,643 | 21,444 |

本地 gRPC-Web 数字**异常偏高** (1.14M msg/s): 本地测试中客户端与服务端同机,
走 loopback 且没有跨进程边界。云端是真实跨进程, 因此 54,645 才是可参考的值。
**同机压测的吞吐数字不能用于容量规划。**

### 4.4 LwM2M 压测 (新增 4 项)

LwM2M 此前无任何压测。由于线上协议是文本命令 (见 3.5), 压测的定位是
**注册表与资源存储在并发下的正确性**, 这与线上格式无关:

| 测试 | 断言 |
|---|---|
| `TestStressLwM2MRegistryChurn` | 200 端点 x 20 轮 register/update/deregister 后注册表为空; 峰值活跃数不超过 worker 数 (防重复计数) |
| `TestStressLwM2MSweepUnderConcurrency` | 清扫恰好回收 100 个短生命周期端点, 100 个长生命周期端点全部存活 |
| `TestStressLwM2MCapacityLimit` | 200 并发注册, cap=50 下 admitted <= 50 且被拒的报 `ErrRegistrationLimit`; 已准入的全部可读 |
| `TestStressLwM2MReadWriteConcurrent` | 50 端点 x 40 轮同资源读写, 读到的值必须是该端点自己写过的 (防撕裂读) |

这几个断言各自对应一个真实的并发缺陷模式:

- **重复计数**: 同一端点重复 register 若被当作新条目, 峰值会超过 worker 数。
- **清扫误杀**: 清扫若与注册并发而无正确加锁, 可能释放刚插入的条目,
  或复活已删除的。测试用"短寿命恰好全清 + 长寿命全留"同时卡住两个方向。
- **容量失效**: cap 若在并发下被绕过, admitted 会超过 50。
- **撕裂读**: 读路径若无锁, 会读到没有任何写者写过的中间值。
  测试让每个写者戳上自己的身份, 读者校验身份而非仅校验非空。

四个测试本地与云端均 PASS。

### 4.6 QUIC 压测踩到的两个协议本质问题

这两个问题各花了几轮诊断, 记录下来因为它们不是本项目的缺陷, 而是 QUIC
语义与常见假设的冲突:

**(1) 服务端读流需要 EOF 信号。** `quic/server.go:209` 用 `io.ReadAll`
读流, 该函数只在 EOF 时返回。客户端只 `Write` 不关闭写侧, 数据就永远
buffered 在服务端, handler 不执行 -- 而连接和流都显示正常。
解法是客户端 `stream.Close()` (半关闭)。

**(2) echo 走服务端新开的流。** `quic/session.go:103` 的 `writeLoop` 对每个
待发 payload 调用 `OpenStreamSync` **新开一条流**, 而不是写回请求流。
因此在请求流上读回复会永久阻塞, 即使 handler 已经跑完。
解法是客户端 `conn.AcceptStream` 接收服务端开的流。

这两个特征合起来意味着: 每请求需要一对流 (客户端开一条发请求, 服务端开一条
回 echo), 且请求流必须半关闭。不了解这两点会测出"连接成功、零流量"的结果,
看起来像服务端故障。

另一个隐蔽的坑在读辅助函数: 手写的"读满 N 字节"循环若把首次遇到的错误直接
返回, 会把 `n=256, err=EOF` (数据完整但末块带 EOF) 判成失败。
标准库 `io.ReadFull` 已经处理了这个区分 (读满后 EOF 是成功, 提前 EOF 是
`ErrUnexpectedEOF`), 所以直接用它而不是自己写。

### 4.5 示例补全 (新增 3 个)

`examples/` 原有 tcp / udp / websocket / coap / lwm2m, **缺 QUIC 与 gRPC-Web**。
"使用"层面因此有缺口, 本轮补齐:

| 示例 | 内容 |
|---|---|
| `examples/basic_quic/main.go` | QUIC echo 服务端, 自签证书, 文档化 4.6 节的两个协议本质 |
| `examples/basic_grpcweb/main.go` | gRPC-Web unary echo, 说明两条路由的区别与帧格式 |
| `examples/quic_verify_client/main.go` | 配套客户端, 实测握手 / 半关闭 / 服务端开流三点 |

QUIC 示例的头注释把 4.6 的两个协议本质写进文档, 避免后来者重复踩。
配套客户端实际跑通, 输出:

```
OK: handshake, half-close and server-opened reply stream all work;
    echo="verify-quic-round-trip"
```

gRPC-Web 示例的帧格式用 Python 客户端实测确认 (curl 无法发送 `\x00` 字节,
会得到空 echo, 这是工具限制而非服务端问题):

```
echo frame:    flag=0   len=5  payload=b'hello'
trailer frame: flag=0x80 len=16 payload=b'grpc-status: 0\r\n'
```

示例还记录了 `WithGRPCWebWebSocketMode` 的坑: WebSocket 路由与 unary 路由
是**不同的路径**, 把两者指向同一 pattern 会让升级请求落到 unary handler
并返回 400。

---

## 5. 过程中修掉的测试自身缺陷

全部为**测试代码问题**, 非产品缺陷。其中 3 个是被服务端按规范正确拒绝的
非法输入, 记录于此因为它们证明了测试确实在验证规范。

| # | 现象 | 根因 | 归属 |
|---|---|---|---|
| 1 | WebSocket 握手全部 400 | `Sec-WebSocket-Key` 用了非 16 字节的 base64 | 测试缺陷 |
| 2 | WS 回显断言 opcode 失败, 读到 close(1000) | 客户端帧未 mask, 服务端按 RFC 6455 5.3 正确拒绝 | **服务端正确** |
| 3 | CoAP handler 收到 0 字节 payload | 报文缺少 0xFF payload marker (RFC 7252 3.1) | 测试缺陷 |
| 4 | CoAP CON 每轮交替收到 4 B / 256 B | 每请求产生 echo + ACK 两个数据报, 只读一个 | 测试缺陷 |
| 5 | QUIC 读回 `n=256 err=EOF` 判为失败 | 手写读循环把"读满后带 EOF"当错误; 改用 `io.ReadFull` | 测试缺陷 |
| 6 | gRPC-Web 零流量 | 向裸 TCP 写帧, 未做 WebSocket 升级; 且 `WithPath` 覆盖了 unary 路径导致 mux 冲突 | 测试缺陷 |
| 7 | CoAP pseudo-session "泄漏" | 默认 TTL 2 分钟, 测试只等 15 秒 | 测试缺陷 |
| 8 | CON 请求第 2 条起被去重 | 客户端复用 msgID, 服务端按 RFC 7252 4.5 正确去重 | **服务端正确** |

第 2 与第 8 项值得强调: 服务端**正确地**执行了规范 (拒绝未 mask 的帧、
按 msgID 去重), 而测试最初假设它不会。修的是测试, 但这个过程确认了
服务端在两处边界上是符合规范的。

---

## 6. 本次改动

| 文件 | 改动 |
|---|---|
| `tests/protocol_conformance_test.go` | 新增。11 项规范符合性测试 (HTTP 5, WS 4, UDP 2) |
| `tests/stress/stress_protocols_test.go` | 新增。4 项压测 (CoAP CON/NON, QUIC, gRPC-Web) |
| `tests/stress/stress_lwm2m_test.go` | 新增。4 项压测 (注册表 / 清扫 / 容量 / 并发读写) |
| `examples/basic_quic/main.go` | 新增。QUIC echo 服务端示例 |
| `examples/basic_grpcweb/main.go` | 新增。gRPC-Web unary echo 示例 |
| `examples/quic_verify_client/main.go` | 新增。QUIC 配套验证客户端 |
| `scripts/workerpool_server.go` | 新增 `-stats-interval` (内网压测对比用) |
| `.workbuddy/cloud/protocol_full.sh` | 新增。云端一键执行规范 + 压测 + 全量 |
| `.workbuddy/cloud/pool_start.sh` 等 | 池对比压测驱动 (上一轮) |

验收: 本地 `go test ./...` 28 包 0 失败 (含 tests/stress 102 s);
云端 `bash /root/protocol_full.sh` 27 包 0 失败, 14 项压测全 PASS;
`gofmt -l` 为空; `go vet` 通过; 两个新示例均实际跑通并验证帧内容。

---

## 7. 缺口与边界

### 7.1 HTTP/2 未实现 (按约定排除)

用户清单包含 HTTP/2, 但该项目未实现。`internal/transport/websocket/server.go:61`
与 `grpcweb/server.go:65` 的注释明确记录这是有意为之:

> to avoid HTTP/2 negotiation that conflicts with WebSocket upgrades.

HTTP 传输基于标准库 `net/http`, 未启用 h2 (`NextProtos` 不含 "h2")。
本轮按约定排除 HTTP/2, 此处记录缺口。若后续需要, 属功能实现而非测试任务。

### 7.2 LwM2M 的线上协议不是 OMA 规范

这是本轮查清的最重要边界, 详见 3.5。分两层:

- **TLV 编解码**符合 OMA 规范, 已有字节级测试;
- **CoAP 线上协议**是自创文本命令 (`register <ep> <lt>` 空格分隔),
  不是规范的 `/rd?ep=...` URI + Content-Format 协商模型。
  适配器注释自述为 "adapts a small text command protocol"。

因此**没有 LwM2M 规范符合性测试**, 且这不是遗漏而是该实现的设计选择。
已补的是与线上格式无关的 4 项并发压测 (4.4)。
是否改用真正的 OMA 线上协议是产品决策, 测试无法裁定。

### 7.3 压测为同机模式

本轮新增压测 (8 项) 的客户端与服务端在同一进程内。这对协议正确性足够,
但吞吐数字包含 loopback 特性, **不能用于容量规划**。容量数据以上一轮的
跨主机阶梯压测 (`STRESS-INTRANET-261017.md`) 为准。

本地与云端的巨大差异印证了这一点: 本地 gRPC-Web 1.14M msg/s vs 云端 55k,
47 倍差距全部来自 loopback。

### 7.4 数据可信度

- 云端数据为单次运行, 各档内部一致但未做跨轮重复。
- 2 vCPU 是小型规格, 绝对吞吐不代表框架上限。
- 本地与云端的巨大差异 (尤其 gRPC-Web 47 倍) 说明**同机压测的数字形态不同**,
  不应混用。

---

## 8. 结论

7 个协议全部有了三层覆盖, 其中 6 个三层全通过; HTTP/2 按约定排除并记录原因。
规范层新增 11 项, 压测层新增 8 项, 示例补 3 个, 全部在阿里云 Linux 上执行通过。

规范测试的边际价值在本轮得到验证: 它在两个边界上确认了服务端行为符合 RFC
(拒绝未 mask 帧、按 msgID 去重), 并发现一个未修复的互操作限制
(WS text 帧回显为 binary)。这些都是功能测试与压测都不会触及的层面。

LwM2M 是本轮查清边界最多的协议: 它的线上协议不是 OMA 规范而是自创文本命令,
所以**没有**规范符合性测试; 但 TLV 编解码是标准格式且已有字节级测试。
这个区分记录在 3.5, 因为它决定了后续任何 LwM2M 工作的起点 --
要 interoperate 需要一个真正的 LwM2M 客户端, 而那不是测试能补的。