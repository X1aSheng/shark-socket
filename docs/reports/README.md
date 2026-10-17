# docs/reports/ 索引

## meta-info

- 用途: 本目录的导航、命名规则与"当前 / 历史"判定
- 最近整理: 2026-10-16
- 命名规则: `<类别>-YYMMDD[-HHMMSS][-<标签>].md`
- 文档规范: 全文仅使用基本 ASCII 字符与中文

## 1. 命名规则

```
<类别>-YYMMDD[-HHMMSS][-<标签>].md
```

**日期在前、标签在后。** 日期在前使目录按时间自然成序; 标签在末尾, 用于区分同一天的
多轮。标签放在日期之前的历史文件名已改回 (`NETWORK-VALIDATION-261016-092259-DIRECT.md`)。
非报告文档的命名规则见 [`docs/README.md`](../README.md) 第 2 节。

类别清单:

| 类别 | 内容 |
| --- | --- |
| `PROJECT-REVIEW` | 项目审查与改进计划 (含缺陷登记表与改进计划) |
| `CLOUD-VALIDATION` | 云端验证: 编译 / 测试 / 竞态 / 漏洞扫描 / 跨主机压测 |
| `DEPLOYMENT-VALIDATION` | 部署验证 (裸机 / Docker / Kubernetes) |
| `NETWORK-VALIDATION` | 跨云主机网络验证 |
| `ARCHITECTURE-ANALYSIS` | 架构分析 |
| `ARCHITECTURE-METHODOLOGY` | 架构方法论 |
| `BENCHMARK` | 性能基准 |
| `GAP-ANALYSIS` | 差距分析 |
| `COMPARISON` | 横向对比与基线补齐 (含资源占用数据) |
| `STRESS-LADDER` | 压测与承载能力测定 |

`PROJECT-REVIEW` 沿用项目既有规则 `PROJECT-REVIEW-YYMMDD-HHMMSS`, 不变。日期相同时
按时间后缀排序即可, 不需要标签。

## 2. 当前与历史

每个类别只保留一份"当前", 其余均视为历史快照。历史报告**不删除**: 逐轮记录是
可追溯性的来源, 且缺陷编号 (D1, D2, ...) 跨报告引用, 删了会断链。

| 类别 | 当前 | 历史 |
| --- | --- | --- |
| 项目审查 | `PROJECT-REVIEW-261016-163106.md` (V10) | V9 `261015-172344` / V8 `260809-091049` / V7 `260808-220224` / V6 `260806-230955` / V5 `260806-034632` / V4 `260626-163000` / V3 `260626-V3` / V2 `260626-230000` / `260615-211500` / `260614-102231` (综合) |
| 云端验证 | `CLOUD-VALIDATION-261016-SRV23.md` | `CLOUD-VALIDATION-261016-V10.md`, `CLOUD-VALIDATION-261016-L2.md` |
| 部署验证 | `DEPLOYMENT-VALIDATION-261016-011918.md` | `260809-085443`, `260807-010639`, `260806-044737`, `260626-173000`, `260626-093000`, `260614-085103` |
| 网络验证 | `NETWORK-VALIDATION-261016-092259-DIRECT.md` (直连) | `NETWORK-VALIDATION-261016-014530.md` (SSH 隧道) |
| 架构分析 | `ARCHITECTURE-ANALYSIS-260626.md` | - |
| 方法论 | `ARCHITECTURE-METHODOLOGY-260626.md` | - |
| 性能基准 | `BENCHMARK-260614-102513.md` | - |
| 差距分析 | `GAP-ANALYSIS-260614.md` | - |
| 横向对比 | `COMPARISON-261017-SMARTBOOT.md` (smart-socket 对比 + 资源基线) | - |
| 压测 | `STRESS-LADDER-261017.md` (供给阶梯 + 承载能力) | - |

### 2.1 资源占用数据的归属

`COMPARISON-261017-SMARTBOOT.md` 补齐了 `BENCHMARK-260614-102513.md` 缺失的资源维度:
每连接常驻堆 6647 B、每连接 2 goroutine、10k 连接栈内存 164.5 MB。

`BENCHMARK-260614-102513.md` 第六节名为"云服务器资源使用", 但记的是主机级指标
(MemAvailable / Load1), 不是被测程序的占用。该节数据本身有效, 只是不能读作资源基线;
资源基线以上述横向对比报告为准。

## 3. 云端验证与主机的对应关系

三份云端验证报告跨越两批不同的主机, 规格差异很大, 读数字前先看这一节。

| 报告 | 主机 | 规格 | 被测 commit | 主机状态 |
| --- | --- | --- | --- | --- |
| `CLOUD-VALIDATION-261016-L2.md` | srv2 + srv3 | 8 vCPU / 16G x2 | `b5fe6f738` | 已释放 |
| `CLOUD-VALIDATION-261016-V10.md` | 120.76.44.233 | 2 vCPU / 1.6GB (共享 k8s 节点) | `0082b4ead` | - |
| `CLOUD-VALIDATION-261016-SRV23.md` | srv2 + srv3 | 8 vCPU / 16G x2 | `659417c84` (验证时) | 已释放 |

**V10 那一份的吞吐数字不能作为性能基准**: 该机是共享 k8s 节点, 验证期间 1 分钟负载
一度达到 80, 且 `govulncheck` 会因内存不足被 OOM 杀死。SRV23 那份用的是专用主机,
吞吐数字才具备参考价值。

srv2 / srv3 于 2026-10-16 晚间释放, 远端日志随实例一并销毁。这两份报告的远端数字
无法再从主机复取, 以文档内记录为准。

## 4. 新主机开好后, 报告落在哪里

1. 在 `docs/reports/` 下新建 `CLOUD-VALIDATION-<YYMMDD>-<标签>.md`
2. 标签取本轮的主题或主机代号, 与现有三份同类 (`L2` / `V10` / `SRV23`) 保持同一形状
3. 同一天跑多轮时, 用不同标签区分, 不要覆盖
4. meta-info 里至少写明: 主机地址与规格、被测 commit、验证时间窗口、远端日志路径
5. 若主机是一次性实例, 验证结束后在 meta-info 补一句"主机已释放, 远端日志随实例
   销毁", 并点明哪些数字无法再复取

meta-info 可照 `CLOUD-VALIDATION-261016-SRV23.md` 的写法。

## 5. 复现说明

驱动脚本位于 `.workbuddy/cloud/` (供给 Go、同步仓库、跑验证、跨主机压测)。
**该目录在 `.gitignore` 内, 不在仓库中**, 因此本目录的读者没有现成脚本可用:
`CLOUD-VALIDATION-261016-SRV23.md` 第 9 节记录了这些脚本的形态与三处修正
(`${PIPESTATUS[0]}`、MSYS 路径改写、变量多层展开), 是重建时的依据。

## 6. 待统一项

- 部分报告 meta-info 内有 `报告编号:` 行, 部分没有。编号与文件名重复, 意义不大,
  新报告可不写。
- 早期报告正文里出现过 `docs/guides/Architecture.md` 与
  `docs/architecture/ARCHITECTURE-FILE-STRUCT.md` 这两个路径。前者已移入
  `docs/archive/`, 后者已改写为 `docs/README.md`; 两处原路径都留了跳转说明,
  历史报告的记载保持原样不做回填。
