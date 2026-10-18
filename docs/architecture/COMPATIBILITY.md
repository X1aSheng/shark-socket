# 兼容承诺

## meta-info

- 用途: 回答"升级会不会把我弄坏", 以及哪些部分根本没有变化的自由
- version: 1.0
- 生效: v0.1.0 起
- 依据: `api/testdata/surface.txt` (208 条导出) + `api/surface_test.go` + 下列 ADR
- 上一级: [`docs/architecture/README.md`](README.md)
- 文档规范: 全文仅使用基本 ASCII 字符与中文

---

## 1. 承诺的边界是编译器画的那条线

本仓库 45 个包: 20 个在 `internal/` 下, 19 个是 `main` (命令与示例), 剩下 6 个里
只有 `api` 一个属于框架 -- 另 5 个 (`tests/*` 与 `scripts/ladmetrics`) 是测试与工具
辅助代码, 不在承诺内。

`internal/` 不是"不推荐使用", 是 import 不了: Go 的 internal 规则在编译期拒绝,
与本文件写不写无关。因此:

- **承诺的对象是一个包, 208 条导出, 零个 internal 路径。**
- 判据很实用: 如果你在 `import ".../internal/..."`, 你已经在承诺之外了。仓库
  内部的代码可以这么写, 仓库外的代码会拿到一句 `use of internal package not
  allowed`, 而不是一句道歉。

208 条的构成 (会随版本增长, 权威数字用 `grep -vc '^#' api/testdata/surface.txt`):

| 类别 | 条数 | 说明 |
| --- | --- | --- |
| `func New*` | 26 | 构造器 |
| `func With*` | 58 | Option 构造器, 全部返回 `XxxOption` |
| 其他 `func` | 3 | `AdaptTyped`, `ParseLwM2MPath`, `Run` |
| `type` | 61 | **全部**是别名 (`type X = internal.Y`), 没有一个新类型 |
| `const` | 11 | 协议常量 7 个, 会话状态 4 个 |
| `var` | 14 | 错误哨兵, 见第 4 节 |
| 接口方法 | 35 | 8 个接口的方法集, 见第 2 节 |

## 2. 三档冻结

| 档 | 内容 | v0.x 内能否改 | 为什么 |
| --- | --- | --- | --- |
| **F0 冻结** | 用户实现的接口方法集 (`Plugin` `Codec` `Logger` `Metrics` `Tracer` `Framer` `Store`, 以及被单测伪造的 `Session`); 错误哨兵; 协议与会话状态常量 | 不能。改就是破坏性变更 | 接口加一个方法, 所有实现与 mock **当场不编译**。这不是"可能影响", 是编译错误 |
| **F1 只增不改** | `New*` / `With*` 构造器; 配置 JSON 的字段名; 指标名与类型; `api` 里的别名目标 | 只可新增; 已有项的**语义**不变 | 调用方已经在用。新增随时可以, 删除与改义不行 |
| **F2 自由重构** | `internal/` 的目录与实现; 具体类型**新增**方法; 日志文案; 错误 message 文本; 性能数字 | 随时 | 外部观察不到, 或者本来就不该被依赖 |

三档的区别不在"重要程度", 在**能不能被别人观察到**。下面三处改动都不改任何公开
名字, 后果却完全不同:

- `Session.Send` 多一个参数 -> F0。改签名, 所有实现与伪造当场不编译。
- `*TCPServer` 多一个方法 -> F2。你拿到的是指针, 只要不删方法就没人受影响。
- `WithTCPAddr` 的行为从"原样 listen"改成"先 trim 再 listen" -> 签名没变, 编译
  没变, 测试也可能全绿, 但语义变了。这是 F1 明确不许的一类, 也是最难查的一类。

### 2.1 两条容易搞错的地方

- **`Handler` 与 `TypedHandler` 是函数类型, 不是接口。**
  `type Handler func(Session, Message) error` 没有方法集可加, 它的形状由签名
  本身决定, 已在 golden 里 (`func(Session, Message) error`)。
- **`SessionManager` 与 `Server` 不在 F0 的接口清单里。**
  判据是"你会不会写 `func (x) Method(...)` 去满足它"。这两个你只收到、不实现。
  `Session` 是例外: 框架实现它, 但 handler 单测几乎总要伪造一个, 所以按 F0 对待。

### 2.2 写这一页时补上的两个真实缺口

冻结清单逼出两个此前没人发现的问题: `api` 没有导出 `Span` 与 `SessionState`。
`Tracer.Start` 返回 `Span`, `Session.State` 返回 `SessionState`, 而
`internal/core` 外部不可 import -- 也就是说 v0.1.0 的 `Tracer` **无法从外部实现**,
`Session` **无法从外部伪造**, 因为签名里的类型在外面没有名字。已补上
(`api.Span` `api.SessionState` 与 4 个状态常量), 并加了别名断言测试 (第 7 节)。

## 3. 冻结的是语义, 不只是签名

签名不变而行为变了, 是最难查的一类破坏: 编译通过, 测试通过, 上线才发现。以下
行为按 F0 处理, 改动必须以新 ADR 取代旧 ADR:

| 行为 | 冻结成什么 | 出处 |
| --- | --- | --- |
| 停机顺序 | `StopAccept` -> `Drain` -> `CloseSessions` -> `Finalize`, 每阶段独立超时, 超时只记录不回滚 | ADR-003 |
| 插件 panic | 单个插件 panic 被隔离, 网关继续服务, 该次操作失败并返回 `ErrPluginPanic` | ADR-004 |
| `OnAccept` 失败 | 已调用过 `OnAccept` 的插件按逆序回滚 `OnClose` | `PLUGIN.md` 2.1 |
| 指标面 | 计时点固定在 handler 边界 (含 handler 自己的 `Send`, 不含运行时开销), 跨协议同口径; 失败与 panic 也计入消息计数 -- 丢包不能表现为流量下降; 直方图是真 `histogram`, 不用 `summary` 假装有分位数 | ADR-020 |
| 写批处理 | 默认关闭; 开启后只改 syscall 次数与**写超时粒度** (从每帧一个 deadline 变成整批共用一个), 不改字节顺序, 不丢不截断 | ADR-013 |
| 准入控制 | 每 IP 配额独立于总量; HTTP 超限答 503, 而不是接受连接后丢弃 | ADR-017 |
| 消息所有权 | `Session.Send` 复制 payload, 返回后调用方即可复用缓冲; 广播走 `SharedFrame` 只是想省掉 N 次复制, 不改变这个语义 | `CONTRACTS.md` 5.4.1, ADR-014 |

## 4. 错误是契约

14 个哨兵在 `api` 里, 跨包 `errors.Is` 直接可用 (是框架自己的值, 不是副本):

| 哨兵 | 谁返回 | 什么时候 |
| --- | --- | --- |
| `ErrSessionClosed` / `ErrServerClosed` / `ErrClosed` | `Session.Send`, server 操作 | 资源已在关闭。三个同义, 主语不同 |
| `ErrWriteQueueFull` | `Session.Send` | 每会话写队列拒收, 帧**没有发出**。只有排队型 (TCP) 会返回 |
| `ErrFrameTooLarge` | 各传输 | 载荷超过该传输的帧上限。永久性错误, 重试无用 |
| `ErrSessionCapacity` | `SessionManager.Register` | 会话索引已满。accept 路径上就是 `MaxConnections` 的效果 |
| `ErrDuplicateSession` / `ErrDuplicateProtocol` | `SessionManager.Register` / `Gateway.Register` | 同一 ID 或同一协议被占 |
| `ErrInvalidArgument` / `ErrNoServers` | 构造与 `Start` | 参数校验失败 |
| `ErrPluginPanic` / `ErrHandlerPanic` | 插件链 / 各 handler 调用点 (`Broadcast`, `CloseAll`, worker pool) | panic 已被隔离并转成错误, 网关仍在服务。后者连"你实现的 `Session` 自己 panic"也包住 |

选择规则只有一条: **能观察到, 或必须产出**。

- 能观察到 = 你调用的方法会返回它。
- 必须产出 = 你从 `Handler` 或 `Plugin` 里返回它, 框架据此改变行为。
  `ErrPluginDrop` 从 `OnMessage` 返回表示"丢弃这条消息, **连接保持**";
  `ErrPluginBlock` 从 `OnAccept` 返回表示拒绝连接。这两个是框架接受的全部控制信号。
  注意不对称: `OnMessage` 返回除 `ErrPluginDrop` 之外的任何错误都会**关闭会话** --
  对失败是合理默认, 对限流器则是灾难。
- `ErrUnsupportedFeature` 在 `internal/core` 里有定义但**没有任何返回点**, 所以
  没导出。导出一个永远不出现的错误, 等于给调用方一个恒假的分支。

完整清单 (含未导出的那些) 在 [`ERRORS.md`](ERRORS.md)。

## 5. 不在承诺内

- **性能数字。** `docs/reports/BENCHMARK-*.md` 是某台机器某天的记录, 不是合同。
  包括"1 连接落后 gnet 32-36%"这条 -- 它是待办, 不是规格。
- **日志文案与错误 message 文本。** 可以按需改。能被判断的是哨兵, 不是字符串;
  写 `strings.Contains(err.Error(), "too large")` 的人在承诺之外。
- **`cmd/shark-socket` 的命令行 flag。** 只增不减, 但 flag 的默认值与输出文本不限。
- **配置文件的未知字段。** 当前被静默忽略 (没有 `DisallowUnknownFields`)。
  好处是升级后加字段不会让旧配置文件失败; 代价是拼错字段名也不报错。这是已知
  取舍, 不是遗漏。
- **`internal/` 的目录树、类型名、算法。** 20 个包可以随时合、拆、改名。
- **入站帧的生命周期。** 今天内置 framer 与 UDP / CoAP 读路径都是每帧 `make` 一次,
  所以把 handler 收到的 `[]byte` 存下来"看起来"能用 -- 但那是当前实现, 不是承诺。
  一旦读缓冲改成池化 (这是性能报告一直盯着的方向), 存下来的切片就会变。要留就自己复制。
- **`tests/*` 与 `scripts/ladmetrics`。** 辅助包, 不承诺。

## 6. 弃用怎么走

- **v0.x**: 先加新的, 再给旧的加 `// Deprecated: <替代品>, 将在 v0.<n+1> 移除`,
  至少保留一个 minor。文档注释是唯一会被工具看见的位置 (IDE 删除线,
  `staticcheck SA1019`), changelog 里的一行不算弃用。
- **v1.0 之后**: SemVer 字面执行, 破坏性变更只发生在大版本。
- **两种情形都要**: `CHANGELOG.md` 里单独一节 Breaking, 且破坏性变更必须配一份
  取代旧决定的 ADR, 说明为什么值得。有 ADR 流程是这类重构能安全做完的前提。

现状: 至今 **0 个 `Deprecated:` 标记**, 因为 v0.1.0 到本文件只有两轮迭代,
没有需要弃用的东西。这一栏现在是空的, 不是免检的。

## 7. 这套承诺怎么被强制

三件事, 都不是"约定":

1. **`internal/` 边界 -- 编译器执行。** 20 个包外部不可 import, 不需要任何人守。
2. **`api/testdata/surface.txt` + `TestPublicSurface` (208 条)。**
   逐条比对导出声明与接口方法集:
   - 删除、改名、改签名 -> 红, 并提示补 ADR 与 Breaking 条目。
   - **新增也红。** 新增是一个新承诺, 得先决定它属于第 2 节哪一档, 再
     `go test ./api -run TestPublicSurface -update-surface`。
   - **改参数名不红。** 参数名不在调用语法里, 谁也观察不到; 让它红只会训练人
     无脑 `-update`, 那是这个测试唯一承受不起的失败模式。
   - 具体类型的新方法、`internal/` 的一切、文档注释改动都不在比对范围。
3. **`TestAliasesPointAtTheInternalTypes` + `TestErrorSentinelsAreTheSameValues`。**
   别名必须还是别名。把 `ErrFrameTooLarge = core.ErrFrameTooLarge` 改成
   `errors.New("frame too large")` 会保住文档、丢掉契约, 而上面所有比对**还是绿的** --
   这两条测试专门堵这个洞。

实测过 (2026-10-18): 加一个导出函数 -> 红并列出 `added`; 把 `WithTCPAcceptRate`
的 `float64` 改成 `int` -> 红并列出 `removed or re-signed` 与 `added` 两行;
把 `WithTCPAddr` 的参数名从 `addr` 改成 `address` -> 绿。

## 8. 升级前自查

1. 我在 `import ".../internal/..."` 吗? -> 停, 那部分没有承诺。
2. 我在比较错误字符串吗? -> 换 `errors.Is` + 第 4 节的哨兵。
3. 我把 handler 收到的 `[]byte` 存下来了吗? -> 复制。只有 `Send` 的入参承诺被复制,
   收进来的帧不承诺 (第 5 节)。
4. 我依赖某个 `With*` 的默认值吗? -> 显式设置。默认值属于 F1 的"只增不改",
   但显式比默认更容易在升级时看清差异。

---

相关: [`CONTRACTS.md`](CONTRACTS.md) (接口形状的权威描述),
[`ERRORS.md`](ERRORS.md) (错误体系全表), [`../decisions/README.md`](../decisions/README.md)
(ADR 索引), `api/testdata/surface.txt` (导出面原文).
