# docs/ 索引

## meta-info

- 用途: 文档总入口, 说明 `docs/` 下各子目录的职责与命名规则
- 最近整理: 2026-10-17
- 文档规范: 全文仅使用基本 ASCII 字符与中文
- 前身: 本文由 `docs/architecture/ARCHITECTURE-FILE-STRUCT.md` 改写而来, 该文只描述
  拆分结果, 现已升级为跨目录的导航与规则

## 1. 目录职责

| 目录 | 放什么 | 索引 |
| --- | --- | --- |
| `architecture/` | 当前架构的**权威描述**: 总览, 契约, 生命周期, 传输, 协议, 插件, 可观测, 安全, 部署, 排障 | [README.md](architecture/README.md) |
| `guides/` | 使用侧的操作指南: 配置, 测试策略, 协议测试, 示例, MQTT 互通 | 见第 3 节 |
| `decisions/` | 长期架构决策记录 (ADR), 一次决策一份 | [README.md](decisions/README.md) |
| `design/` | 在途设计方案, 尚未并入 `architecture/` | 见第 3 节 |
| `planning/` | 项目计划与实施目标 (定稿快照) | 见第 3 节 |
| `reports/` | 审查, 部署验证, 云端验证, 基准测试的记录, 逐轮留档不删 | [README.md](reports/README.md) |
| `archive/` | 已被取代或已执行完毕的文档, 不作现行依据 | [README.md](archive/README.md) |

判断归属的一句话标准: **写"现在是什么"进 `architecture/`, 写"怎么用"进 `guides/`,
写"为什么这么定"进 `decisions/`, 写"跑出来是什么"进 `reports/`。**

## 2. 命名规则

两套规则, 按目录适用:

| 目录 | 规则 | 示例 |
| --- | --- | --- |
| `reports/` | `<类别>-YYMMDD[-HHMMSS][-<标签>].md` | `CLOUD-VALIDATION-261016-SRV23.md` |
| 其余目录 | `UPPER-CASE-WORDS.md`, 定稿快照追加 `-YYMMDD` | `CONTRACTS.md`, `CONFIGURATION-20260530.md` |

两条共同约束:

1. **日期在前, 标签在后。** 日期在前使目录按时间自然成序; 标签在末尾, 用于区分同一天
   的多轮。标签放在日期之前的历史文件名 (`NETWORK-VALIDATION-DIRECT-261016-092259.md`)
   已在本次整理中改为 `NETWORK-VALIDATION-261016-092259-DIRECT.md`。
2. **持续维护的文档不带日期, 定稿快照带 `-YYMMDD`。** 带日期即表示内容冻结在该日,
   后续若要长期更新, 应另起文件或去掉日期后缀。

## 3. 未设独立索引的目录

### 3.1 guides/

| 文件 | 内容 |
| --- | --- |
| [`CONFIGURATION-20260530.md`](guides/CONFIGURATION-20260530.md) | 配置字段, 默认值, 环境变量, 验证规则 |
| [`TEST-STRATEGY-20260529.md`](guides/TEST-STRATEGY-20260529.md) | 测试层次, 回归清单, fuzz, benchmark, 验收命令 |
| [`PROTOCOL-TEST-GUIDE-20260530.md`](guides/PROTOCOL-TEST-GUIDE-20260530.md) | 各协议的测试步骤与判定方式 |
| [`EXAMPLES.md`](guides/EXAMPLES.md) | 示例索引 |
| [`MQTT-INTEGRATION.md`](guides/MQTT-INTEGRATION.md) | MQTT 外部 broker 互通契约 |
| [`Architecture.md`](guides/Architecture.md) | 已归档 stub, 原文档在 [`archive/Architecture.md`](archive/Architecture.md) |

### 3.2 design/

| 文件 | 内容 |
| --- | --- |
| [`REARCHITECTURE-261016-v2.md`](design/REARCHITECTURE-261016-v2.md) | 传输内核与运行时解耦的重构方案, version 2.1 |
| [`REARCHITECTURE-261016.md`](design/REARCHITECTURE-261016.md) | 已归档 stub, v1 方案在 [`archive/REARCHITECTURE-261016.md`](archive/REARCHITECTURE-261016.md) |
| [`WORKER-ELASTICITY-261016.md`](design/WORKER-ELASTICITY-261016.md) | 已归档 stub, 原始留档在 [`archive/WORKER-ELASTICITY-261016.md`](archive/WORKER-ELASTICITY-261016.md) |

### 3.3 planning/

| 文件 | 内容 |
| --- | --- |
| [`PROJECT-PLAN-20260529.md`](planning/PROJECT-PLAN-20260529.md) | 项目计划 |
| [`IMPLEMENTATION-GOALS-20260530.md`](planning/IMPLEMENTATION-GOALS-20260530.md) | 实施目标 |

## 4. 内容归属规则

- 总览, 入口和导航写入 `architecture/ARCHITECTURE.md`。
- 依赖是否合法只看 `architecture/ARCHITECTURE.md`。
- 核心接口先更新 `architecture/CONTRACTS.md`, 再改代码。
- Gateway 生命周期问题写入 `architecture/GATEWAY.md` 和 `architecture/LIFECYCLE.md`。
- TCP/UDP/HTTP/WebSocket/CoAP/QUIC/gRPC-Web 写入 `architecture/TRANSPORT.md`。
- LwM2M 和未来应用协议写入 `architecture/PROTOCOL.md`。
- 配置字段只写入 `guides/CONFIGURATION-20260530.md`。
- 测试命令和回归清单写入 `guides/TEST-STRATEGY-20260529.md`。
- 具体验证流水账写入 `reports/` 下的报告, 不写入主架构文档。
- 影响长期边界的选择必须新增或更新 `decisions/ADR-*.md`。
- 已被取代的文档移入 `archive/`, 原路径留跳转 stub, 见 `archive/README.md`。

## 5. 完成度检查

每份文档写完后检查:

- 职责是否单一。
- 是否与其他专题文件大段重复。
- 是否引用正确的上游或下游文档 (路径存在, 且指向现行文档而不是已归档的)。
- 是否包含必要的验收或检查清单。
- 文中出现的类型名, 函数名, 文件路径是否与磁盘上的代码一致。
