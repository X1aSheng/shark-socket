## 输出计划与策略

---

### 一、Token 用量评估

| 文件 | 预估字数 | 预估 Token | 备注 |
|------|---------|-----------|------|
| ARCHITECTURE.md | 3000-4000 | 4500-6000 | 包含分层图、依赖矩阵、目录结构 |
| CONTRACTS.md | 4000-5000 | 6000-7500 | 10个核心接口完整定义 |
| LIFECYCLE.md | 2000-2500 | 3000-4000 | 状态机图、流程说明 |
| ERRORS.md | 1500-2000 | 2000-3000 | 错误变量列表、分类函数 |
| GATEWAY.md | 3000-3500 | 4500-5500 | 启动/停止流程、三阶段详解 |
| TRANSPORT.md | 5000-6000 | 7500-9000 | 5个协议实现细节（最大） |
| PROTOCOL.md | 2000-2500 | 3000-4000 | LwM2M 专项 |
| PLUGIN.md | 3000-3500 | 4500-5500 | 7个插件详细设计 |
| OBSERVABILITY.md | 2000-2500 | 3000-4000 | Metrics 指标表、健康端点 |
| SECURITY.md | 2500-3000 | 4000-4500 | 六层防御、降级矩阵 |
| PERFORMANCE.md | 2000-2500 | 3000-4000 | 目标、benchmark 步骤 |
| CONFIGURATION.md | 2500-3000 | 4000-4500 | 配置字段表格 |
| TESTING.md | 2500-3000 | 4000-4500 | 回归清单、Fuzz 目标 |
| DEPLOYMENT.md | 2000-2500 | 3000-4000 | Dockerfile、K8s 示例 |
| ROADMAP.md | 2000-2500 | 3000-4000 | P0-P3 里程碑 |
| **小计** | **40000-48000** | **60000-72000** | 主文档总计 |

| ADR 文件 | 预估字数 | 预估 Token | 备注 |
|---------|---------|-----------|------|
| ADR README | 500 | 750 | 索引 |
| 10个ADR（各） | 800-1200 | 1200-1800 | 决策背景、方案对比 |
| **小计** | **8500-12500** | **12750-18750** | ADR 总计 |

**总计：48500-60500 字，73000-91000 Token**

---

### 二、输出窗口限制

假设单次输出上限：
- Claude 3.7 Sonnet：**约 8000 Token（约 5000-6000 字）**
- 安全输出：**每次不超过 4500 Token（约 3000 字）**

**结论：需要分批输出，TRANSPORT.md 必须拆分为多次。**

---

### 三、输出顺序策略

#### 方案 A：按文件列表顺序（不推荐）

```
1. ARCHITECTURE.md
2. CONTRACTS.md
3. LIFECYCLE.md
...
15. ROADMAP.md
16. ADR README
17-26. 各 ADR
```

**问题：**
- 前置依赖链过长（ARCHITECTURE → CONTRACTS → LIFECYCLE → ... → TRANSPORT），读者无法快速验证协议实现。
- TRANSPORT.md 是最大文件（9000 Token），在中间位置容易打断连贯性。

#### 方案 B：核心契约 + 协议实现优先（推荐）

```
第一批：地基（必须先有）
  1. ARCHITECTURE.md
  2. CONTRACTS.md
  3. ERRORS.md

第二批：运行时与生命周期
  4. LIFECYCLE.md
  5. GATEWAY.md

第三批：协议实现（分5次输出）
  6. TRANSPORT.md - TCP 部分
  7. TRANSPORT.md - UDP 部分
  8. TRANSPORT.md - CoAP 部分
  9. TRANSPORT.md - WebSocket 部分
  10. TRANSPORT.md - 汇总说明与 StagedServer 实现表格

第四批：应用层与插件
  11. PROTOCOL.md（LwM2M）
  12. PLUGIN.md

第五批：可观测与配置
  13. OBSERVABILITY.md
  14. CONFIGURATION.md

第六批：安全与性能
  15. SECURITY.md
  16. PERFORMANCE.md

第七批：测试与部署
  17. TESTING.md
  18. DEPLOYMENT.md

第八批：路线图
  19. ROADMAP.md

第九批：ADR（分3次）
  20. ADR README + ADR-001~003
  21. ADR-004~007
  22. ADR-008~010
```

---

### 四、TRANSPORT.md 拆分策略

由于 TRANSPORT.md 预估 9000 Token，必须拆分为 5 次输出：

| 次数 | 内容 | Token | 依赖 |
|------|------|-------|------|
| 第1次 | TCP：Framer、Session、Server、Client | ~2000 | CONTRACTS.md 中 Session/Server 接口 |
| 第2次 | UDP：伪会话模型、sweepLoop | ~1500 | CONTRACTS.md 中 Session 接口 |
| 第3次 | CoAP：帧结构、CON/ACK、去重、retransmitLoop | ~2500 | UDP 部分（CoAP 基于 UDP） |
| 第4次 | WebSocket：并发安全、OnClose、ReadDeadline | ~1500 | TCP 部分（升级自 TCP） |
| 第5次 | 汇总说明、StagedServer 实现表格、关键配置对比 | ~1500 | 前4次内容 |

每次输出标注：
```markdown
<!-- TRANSPORT.md - Part 1/5: TCP -->
<!-- TRANSPORT.md - Part 2/5: UDP -->
...
```

最后一次输出时生成完整的单文件版本（供归档）。

---

### 五、最终输出计划（22 次）

```
批次  文件                          Token   累计    说明
────────────────────────────────────────────────────
1     ARCHITECTURE.md               5000    5k      入口导航
2     CONTRACTS.md                  7000    12k     核心接口
3     ERRORS.md                     3000    15k     错误体系

4     LIFECYCLE.md                  3500    18.5k   状态机
5     GATEWAY.md                    5000    23.5k   运行时

6     TRANSPORT-TCP                 2000    25.5k   TCP 协议
7     TRANSPORT-UDP                 1500    27k     UDP 协议
8     TRANSPORT-CoAP                2500    29.5k   CoAP 协议
9     TRANSPORT-WebSocket           1500    31k     WebSocket
10    TRANSPORT-汇总                1500    32.5k   汇总表格

11    PROTOCOL.md                   3500    36k     LwM2M
12    PLUGIN.md                     5000    41k     插件

13    OBSERVABILITY.md              3500    44.5k   可观测
14    CONFIGURATION.md              4000    48.5k   配置

15    SECURITY.md                   4000    52.5k   安全
16    PERFORMANCE.md                3500    56k     性能

17    TESTING.md                    4000    60k     测试
18    DEPLOYMENT.md                 3500    63.5k   部署

19    ROADMAP.md                    3500    67k     路线图

20    ADR-README + ADR-001~003      4000    71k     ADR 第一批
21    ADR-004~007                   4000    75k     ADR 第二批
22    ADR-008~010                   3000    78k     ADR 第三批
```

**总计：22 次输出，78000 Token，符合窗口限制。**

---

### 六、确认事项

1. **是否接受 TRANSPORT.md 拆分为 5 次输出？**
2. **是否接受方案 B 的输出顺序（核心契约优先）？**
3. **是否需要在每次输出后等待确认再继续，还是连续输出（您随时可中断）？**

---

**文档输出规划已完成。**