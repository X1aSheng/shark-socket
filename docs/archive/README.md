# docs/archive/ 索引

## meta-info

- 用途: 存放已被取代或已执行完毕的文档, 不再作为现行依据
- 最近整理: 2026-10-17
- 规则: 归档文件只增不改; 原路径留一行跳转说明, 保证历史引用不断链
- 文档规范: 全文仅使用基本 ASCII 字符与中文

## 1. 归档清单

| 文件 | 归档原因 | 取代者 |
| --- | --- | --- |
| `Architecture.md` | 双语旧版长文档, 与 `docs/architecture/` 下的分专题文档重复 (V9 审查 H15 / L9) | [`docs/architecture/`](../architecture/README.md) 各专题文档 |
| `REARCHITECTURE-261016.md` | v1 重构方案已被 v2 取代 | [`REARCHITECTURE-261016-v2.md`](../design/REARCHITECTURE-261016-v2.md) |
| `WORKER-ELASTICITY-261016.md` | L2 弹性工作池原始留档, 内容已并入 v2 第 11 节 | [`REARCHITECTURE-261016-v2.md`](../design/REARCHITECTURE-261016-v2.md) 第 11 节 |
| `OUTPUT-PLAN.md` | 文档输出计划已执行完毕, 仅为历史记录 | [`docs/README.md`](../README.md) |

## 2. 归档后原路径怎么处理

每个归档文件在原来的位置留一个同名 stub, 内容只有标题和一行跳转说明。这样历史报告里
写的 `docs/guides/Architecture.md` 这类路径不会变成断链, 读者也能立刻知道该去哪。

## 3. 注意

归档文件里的数字与结论可能已经过时, **不要**作为当前实现的依据。只有在需要追溯
"当时为什么这么定"时才读它们。当前依据见 [`docs/README.md`](../README.md)。
