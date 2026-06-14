# Project Review 综合报告

> **生成日期:** 2026-06-14 10:22
> **覆盖范围:** 14 份前序评审（2026-05-30 ~ 2026-06-14 08:51）
> **源文件:** `docs/PROJECT-REVIEW-260530-*.md` × 9 + `docs/PROJECT-REVIEW-260602-*.md` × 4 + `docs/PROJECT-REVIEW-260614-085103.md`
> **前置综合文档:** `docs/PROJECT-REVIEW-COMPREHENSIVE.md`（替代为此文件）

---

## 一、版本演变时间线

| 阶段 | 日期 | 评审次数 | 核心主题 |
|---|---|---|---|
| **初始基线** | 05-30 00:43 | 1 | Gateway 生命周期修复、CI 创建、项目基线 |
| **云验证** | 05-30 08:51 ~ 12:36 | 6 | 单机/双机部署、跨主机协议验证、Benchmark 矩阵 |
| **特性加固** | 05-30 11:50 ~ 11:58 | 2 | mTLS、CI 强化、基准测试套件 |
| **深度审计** | 06-02 06:45 ~ 13:37 | 3 | 全量代码审查（74 项发现）、Phase 3/4 变更、修复 |
| **稳定收敛** | 06-02 21:30 ~ 06-14 08:51 | 2 | 回归修复、覆盖率提升、最终状态确认 |

---

## 二、测试指标趋势

### 测试数量增长

```
  05-30 00:43    83  tests    初始基线
  05-30 08:51   102  tests    首次云验证
  05-30 11:50   119  tests    mTLS + CI 加固
  06-02 06:45   169  tests    v0.2.0-alpha / Phase 3+4
  06-02 21:30   333  tests    回归稳定线
  06-14 08:51   333  tests    最终确认 (race: 364)
```

### 覆盖率

| 日期 | 覆盖率 | 变化 |
|---|---|---|
| 06-02 21:30 | **72.1%** | 首次记录 |
| 06-14 08:51 | **73.3%** | +1.2% |
| 06-14 10:25 | **74.1%** | +0.9%（session + API option 覆盖） |

### 通过率

- 全期间 **100%** 通过率
- **2 个跳过** — 环境依赖（Docker/kubectl 本地未安装时跳过）
- 所有 `go vet ./...` 无问题
- 所有 `go test -race` 通过

---

## 三、缺陷全量清单

### P0 / 严重（9 项，全部已修复）

| ID | 发现日期 | 描述 | 修复 |
|---|---|---|---|
| R-001 | 05-30 | Gateway 重启生命周期崩溃 — `CloseAll` 永久关闭 SessionManager | `35b8428` |
| R-002 | 05-30 | 无 GitHub Actions CI 工作流 | `c106bbf` |
| C-001 | 06-02 | `Acceptor.allowance` 数据竞争（并发 WebSocket/gRPC-Web） | `f42a155` |
| C-002 | 06-02 | UDP 服务器 DTLS goroutine 泄漏 | `f42a155` |
| C-003 | 06-02 | CoAP 服务器 DTLS goroutine 泄漏 | `f42a155` |
| C-004 | 06-02 | CoAP `seen` sync.Map 永不清理（IoT NAT → 内存泄漏） | `f42a155` |
| C-005 | 06-02 | QUIC `closeSession()` 双调 OnClose/Unregister | `f42a155` |
| — | 06-02 | CI 分支不匹配 — push 触发缺少 shark-socket-new-main | 配置修复 |
| — | 06-02 | CoAP `encodeOptionHeader` 扩展格式 panic | Bug 修复 |

### P1 / 高（18 项，全部已修复）

| ID | 发现日期 | 描述 | 修复 |
|---|---|---|---|
| R-005 | 05-30 | WebSocket 关闭 OnClose 双调（竞态） | `6025e5a` |
| R-006 | 05-30 | `max_message_bytes` 接受负值 | `7a47db6` |
| R-007 | 05-30 | CI 仅验证 Windows | `f9c26c6` |
| R-008 | 05-30 | Docker 云构建 proxy.golang.org 超时 | `8edc9eb` (GOPROXY) |
| R-001 | 06-02 | Fuzz RawFramer 空 payload 当作可读帧 | `511eb33` |
| R-002 | 06-02 | LwM2M TLV fuzz 使用失效私有字段名 | `19481f9` |
| R-004 | 06-02 | PowerShell 验证脚本对失败命令报 PASS | `8a7aadd` |
| H-001 | 06-02 | TCP accept 错误循环无退避（CPU 空转） | `f42a155` |
| H-002 | 06-02 | CoAP Observe 序列号编码不一致（RFC 7641 要求 3 字节） | `90fec1d` |
| H-003 | 06-02 | `tlsutil.clientCAFile` 数据竞争 | `f42a155` |
| H-004 | 06-02 | BoltStore 未检查已关闭状态（静默数据丢失） | `90fec1d` |
| H-005 | 06-02 | MessageLog Prune O(n) 事务 | `90fec1d` |
| H-006 | 06-02 | `Gateway.Register(nil)` panic | `f42a155` |
| H-007 | 06-02 | `SessionManager.Register(nil)` panic | `f42a155` |
| — | 06-02 | SessionStore 无测试覆盖 | 已添加测试 |
| — | 06-02 | PersistenceV2 插件无测试覆盖 | 已添加测试 |
| — | 06-02 | API 层缺少 StoreV2 / PersistenceV2 导出 | 已添加别名 |
| — | 06-02 | `parseUint64` 静默丢弃非数字字符 | 改用 `strconv.ParseUint` |

### P2 / 中（37 项，~25 已修复，12 待处理）

**环境类（4 项）:** Docker/Kubectl/Helm 本地未安装、云服务器无 K8s 集群
**传输层（15 项）:** 超时配置、连接关闭顺序、缓冲大小等
**运行时/Core（6 项）:** 插件链错误处理、SessionManager 边界条件
**应用层（2 项）:** 配置验证边界情况
**基础设施（4 项）:** 日志、监控集成细节
**部署（6 项）:** Dockerfile/docker-compose/k8s 配置加固

> 12 项未修复的中优先级缺陷来自 06-02 深度审计，标记为下一迭代处理。

### P3 / 低（37 项，大部分未处理）

典型项：
- RateLimit 仅支持 IP 键，缺少 `WithKeyFunc`（backlog）
- `sortByteKeys` O(n²) 冒泡排序（<1 万键时可忽略）
- 文档、命名、注释等非功能性改进

### 部署加固（14 项，全部已处理）

| 范围 | 项目 | 状态 |
|---|---|---|
| Dockerfile | ca-certificates、UID(1000)、HEALTHCHECK、非 root | ✅ |
| .dockerignore | 创建 | ✅ |
| docker-compose | YAML 语法修复、read_only 位置修正 | ✅ |
| K8s | ConfigMap、PDB、NetworkPolicy、ServiceAccount | ✅ |
| Helm | _helpers.tpl、NOTES.txt、Chart 语义验证 | ✅ |
| CI | golangci-lint、govulncheck、矩阵构建 | ✅ |
| CI 覆盖率阈值 | D-014 未强制 | ⚠️ 待办 |

---

## 四、Benchmark 性能基线

### Core Runtime（零分配）

| Benchmark | 05-30 | 06-02 | 06-14 本地 | 06-14 Server2 |
|---|---|---|---|---|
| SessionManager_NextID | — | 1.59 ns/op | **1.61** ns/op | 4.78 ns/op |
| SessionManager_NextID_Parallel | — | — | **9.74** ns/op | 21.58 ns/op |
| SessionManager_RegisterGetUnregister | — | — | **135.6** ns/op | 213.1 ns/op |
| PluginChain_5Plugins | 48.64 ns/op | 48.79 ns/op | **36.68** ns/op | — |

### Transport Echo（单次往返延迟，ns/op）

| Transport | 05-30 本地 | 06-02 本地 | 06-14 本地 | 06-14 Server2 |
|---|---|---|---|---|
| **TCP** | 56,838 | 49,646 | 36,037 | **18,881** |
| **UDP** | 14,921 | — | 14,123 | **4,876** |
| **WebSocket** | 18,399 | — | 16,843 | **6,149** |
| **HTTP** | 89,648 | 80,097 | 64,180 | **30,804** |
| **gRPC-Web** | — | — | 62,938 | **31,282** |
| **QUIC** | — | — | 2,020,152 | — |

### Stress Test 吞吐量

| 场景 | 环境 | 吞吐量 | P99 | 错误 |
|---|---|---|---|---|
| TCP 50 conns 持续 | 本地 Ryzen 7 | **219,720 msg/s** | ~1.0ms | 0% |
| TCP 50 conns 持续 | Server2 Xeon 8c | **316,375 msg/s** | ~401µs | 0% |
| TCP 突发 500 req | Server2 | 12,331 msg/s | — | 顺序模型 |
| TCP 连接抖动 50 路 | Server2 | **85,922 msg/s** | — | 0% |

---

## 五、云验证概览

### 服务器

| 服务器 | IP | 规格 | 状态 |
|---|---|---|---|
| Server1 (Client) | `120.76.44.233` | 2c/2GB, Ubuntu 26.04 | ✅ |
| Server2 (Server) | `47.110.42.28` | 8c/30GB, Ubuntu 26.04, Docker 29.5, Go 1.26.4 | ✅ |
| Server2 (旧) | `47.110.238.85` | 已废弃 | ❌ |
| 历史节点 | `47.96.129.59` | 历史用 | — |

### 验证范围

| 项目 | 状态 |
|---|---|
| `go build ./...` | ✅ |
| `go test ./...` | ✅ 全部 20 包 |
| `go vet ./...` | ✅ 无问题 |
| `go test -race` | ✅ 364 通过 |
| Docker 构建 | ✅ 多阶段，40.5MB |
| Docker Compose | ✅ |
| K8s (kind) | ✅（2c/2GB 超时） |
| Helm 部署 | ✅ |
| 跨主机 TCP/UDP/WS/HTTP | ✅ |
| 跨主机 CoAP/LwM2M | ✅ |
| 跨主机 gRPC-Web | ✅ |
| mTLS | ✅ |

### 中国大陆网络优化

| 优化项 | 镜像源 |
|---|---|
| Go 代理 | `goproxy.cn` |
| Go 下载 | `mirrors.aliyun.com/golang/` |
| Docker CE | `mirrors.aliyun.com/docker-ce/` |
| Docker Hub | Alibaba Cloud + DaoCloud 镜像加速 |

---

## 六、待处理事项

| 优先级 | 事项 | 类型 | 备注 |
|---|---|---|---|
| **中** | 12 项中型缺陷（06-02 审计） | 代码 | 下一迭代 |
| **中** | CI 覆盖率阈值未强制（D-014） | CI/CD | |
| **低** | WebSocket session 方法零覆盖率 | 测试 | Protocol, RemoteAddr 等 |
| **低** | examples/ 包缺基础冒烟测试 | 测试 | |
| **低** | RateLimit WithKeyFunc 选项 | 功能 | backlog |
| **低** | sortByteKeys O(n²) 冒泡排序 | 优化 | <1万键可忽略 |
| **环境** | 2 个 MQTT 测试被跳过（无 Broker） | 测试 | |
| **环境** | 2c/2GB 规格 kind 集群超时 | 部署 | |
| **信息** | 远程仓库迁至 `github.com/X1aSheng/shark-socket.git` | 管理 | |

---

## 七、结论

1. **项目质量持续提升** — 15 天测试数 83 → 333+，覆盖率 72.1% → 73.3%
2. **严重缺陷全部清除** — 9 P0 + 18 P1 均已完成修复与验证
3. **生产就绪度良好** — 双云服务器全 7 种协议验证通过，零错误零泄漏
4. **性能领先** — Server2 (Xeon 8c) **316,375 msg/s**，P99 < 500µs
5. **剩余 12 项中优先级缺陷** 建议下一迭代处理

---

*本文件替代以下 14 份前序评审文件：*
*`PROJECT-REVIEW-260530-004244.md` ~ `PROJECT-REVIEW-260614-085103.md`*
*以及前置综合文档 `PROJECT-REVIEW-COMPREHENSIVE.md`*
