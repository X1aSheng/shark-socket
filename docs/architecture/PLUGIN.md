# PLUGIN.md

> Shark-Socket 插件层详细设计  
> 版本:v0.2.x-alpha  
> 最后更新:2026-06-01

---

## 目录

1. [概述](#1-概述)
2. [插件执行规则](#2-插件执行规则)
3. [内置插件 Priority 表](#3-内置插件-priority-表)
4. [BlacklistPlugin](#4-blacklistplugin)
5. [RateLimitPlugin](#5-ratelimitplugin)
6. [AutoBanPlugin](#6-autobanplugin)
7. [HeartbeatPlugin](#7-heartbeatplugin)
8. [PersistencePlugin](#8-persistenceplugin)
9. [ClusterPlugin](#9-clusterplugin)
10. [SlowHandlerPlugin](#10-slowhandlerplugin)
11. [自定义插件指南](#11-自定义插件指南)

---

## 1. 概述

### 1.1 插件系统设计原则

| 原则 | 实现 |
|------|------|
| 单一职责 | 每个插件只负责一个横切关注点 |
| 优先级明确 | Priority 数字越小越先执行,`Append` 时稳定排序 |
| panic 隔离 | `PluginChain` 的 `safe*` 包装捕获所有 panic,不影响其他插件 |
| 可观测 | 插件自行上报执行时间与错误次数,链不代劳 |
| 不去重 | `Append` 顺序追加,同名插件共存,名称唯一性由调用方负责 |
| 幂等关闭 | 所有有状态插件的 Close 通过 sync.Once 保证幂等 |

### 1.2 插件与 PluginChain 的职责分离

```
PluginChain 负责(详见 GATEWAY.md sec.4):
  - 按 Priority 稳定排序(Append 时一次完成)
  - 按顺序调用各插件方法
  - 保证回调在锁外执行(copy-on-write 快照)
  - panic 隔离(每个钩子一个 safe* 包装,返回 ErrPluginPanic)
  - OnAccept 中途失败时逆序回滚已调用过的插件
  - StartAll / StopAll 的启动顺序,回滚与错误汇总

各插件负责:
  - 具体业务判断(黑名单匹配,令牌桶计算等)
  - 返回正确的控制错误(ErrPluginDrop / ErrPluginBlock)
  - 自身资源的生命周期管理
  - 自身指标的记录(不依赖 PluginChain 代劳)
```

### 1.2.1 生命周期插件(Start/Stop)

需要后台 goroutine 的插件(**AutoBan**,**RateLimit**,**Heartbeat**,
**Cluster**)实现可选的 `core.LifecyclePlugin`(`Start() error` /
`Stop() error`),用于启动/停止其清理,扫荡或消费循环(过期封禁与计数
回收,心跳扫荡,跨节点消息消费).

- 经 `api.WithPlugins` 挂载后,`Gateway.Start` 会在任何服务 accept 流量
  之前按优先级顺序调用 `Start()`(失败则回滚已启动插件并拒绝启动);
  `Gateway.Stop` 会在所有会话关闭后逆序调用 `Stop()`.无需手工调用.
- 被动插件(只实现 `OnAccept/OnMessage/OnClose`)被自动跳过.
- 脱离 Gateway 单独使用插件链时须自行 `Start()/Stop()`;内置生命周期辅助
  (`internal/plugin/lifecycle.go`)保证重复调用,Stop-without-Start 安全.

**panic 隔离与错误汇总(V9 增强):**

`Start` / `Stop` 与 `OnAccept` / `OnMessage` / `OnClose` 一样是用户回调,因此走同一套
隔离机制.`PluginChain` 为五个钩子各准备一个包装:

| 包装 | 钩子 | panic 后 |
|------|------|----------|
| `safeStart` | `Start` | 记录日志,返回 `core.ErrPluginPanic`,`StartAll` 触发回滚 |
| `safeStop` | `Stop` | 记录日志,返回 `core.ErrPluginPanic`,`StopAll` 继续处理其余插件 |
| `safeAccept` | `OnAccept` | 记录日志,返回 `core.ErrPluginPanic`,链逆序回滚 |
| `safeMessage` | `OnMessage` | 记录日志,返回 `core.ErrPluginPanic`,链终止 |
| `safeClose` | `OnClose` | 记录日志后继续下一个插件 |

此前 `Start` / `Stop` 没有任何 recover:插件后台循环的启动 panic 会绕过网关的错误路径
直接终止进程.现在它表现为一个普通的启动失败,由 `Gateway.Start` 返回给调用方.

错误聚合语义:

- `StartAll` 按优先级正序启动;第 k 个插件失败时,前 k-1 个已启动插件按**逆序**收到 `Stop`,
  回滚自身的失败也一并汇总(因为"启动失败后仍有插件在跑"这件事必须对调用方可见).
- `StopAll` 按逆序停止全部插件,`errors.Join` 汇总各自失败,单个插件失败不会阻止其余插件停止.
- 空链、未启动就调 `StopAll`、重复调用都是安全 no-op.

**链本身不调用 `sess.Close()`:** `PluginChain` 只表达"拒绝"语义(返回错误),关闭会话的
具体动作由传输层决定,这样同一套插件链可以服务于 TCP、UDP、CoAP 等关闭代价不同的传输.

### 1.3 文件清单

```
internal/plugin/
+-- base.go          # BasePlugin 空实现
+-- blacklist.go     # BlacklistPlugin
+-- ratelimit.go     # RateLimitPlugin
+-- autoban.go       # AutoBanPlugin
+-- heartbeat.go     # HeartbeatPlugin
+-- persistence.go   # PersistencePlugin
+-- cluster.go       # ClusterPlugin
+-- slowhandler.go   # SlowHandlerPlugin
+-- options.go       # 各插件 Functional Options
```

---

## 2. 插件执行规则

### 2.1 OnAccept 执行规则(详见 GATEWAY.md)

```
按 Priority 升序执行:
  nil -> 继续下一个插件
  ErrPluginBlock -> 立刻返回该错误,链不继续;关闭动作由传输层执行
  普通 error     -> 立刻返回该错误,链不继续
  panic          -> safeAccept recover,记录 Error,返回 ErrPluginPanic

任一插件返回错误时,序号在它之前且已成功执行的插件按逆序收到 OnClose,
释放它们在 OnAccept 中分配的资源(逆序回滚).
```

链内没有 stopOnError 之类的开关:错误一律终止链并上抛,由传输层决定是关闭会话还是只记录.
行为只有一种,便于推理与测试.

### 2.2 OnMessage 执行规则(payload 改写链)

```
data = originalPayload
按 Priority 升序执行:
  nil, out -> data = out(允许改写 payload),继续下一个插件
  ErrPluginDrop -> 立刻返回,不调用 Handler,连接继续
  ErrPluginBlock -> 立刻返回,连接关闭由传输层执行
  普通 error     -> 立刻返回该错误
  panic          -> safeMessage recover,记录 Error,返回 ErrPluginPanic
返回最终 data 给传输层,再交给 Handler
```

返回错误时 `OnMessage` 返回 `nil` payload,避免调用方把被丢弃的消息当作有效数据继续处理.

### 2.3 OnClose 执行规则

```
按 Priority 逆序执行(确保依赖方先释放):
  每个插件都必须被调用(不可中断)
  即使某个插件 panic 也继续调用后续插件(safeClose 只记日志,不改控制流)
  返回值为 void,错误无法表达也不被观察
```

### 2.4 控制错误语义速查

| 错误 | OnAccept 效果 | OnMessage 效果 |
|------|--------------|----------------|
| `ErrPluginBlock` | 返回错误,链回滚,传输层关闭 Session | 返回错误,传输层关闭 Session |
| `ErrPluginDrop` | 不适用 | 丢弃消息,Session 继续 |
| 普通 error | 返回错误,链回滚 | 返回错误,链终止 |
| `ErrPluginPanic` | 链回滚(包装层产生) | 链终止(包装层产生) |
| nil | 继续 | 继续,使用新 payload |

---

## 3. 内置插件 Priority 表

| Priority | 插件 | 职责 | 典型触发时机 |
|----------|------|------|------------|
| 0 | BlacklistPlugin | IP/CIDR 黑名单过滤 | OnAccept(最早拦截) |
| 5 | AutoBanPlugin | 自动封禁(触发阈值后加入黑名单) | OnAccept + Record() |
| 10 | RateLimitPlugin | 滑动窗口消息限流 | OnMessage |
| 50 | HeartbeatPlugin | 心跳超时检测与清理 | 定时 Sweep |
| 90 | PersistencePlugin | 会话生命周期事件持久化 | OnAccept + OnClose |
| 95 | ClusterPlugin | 跨节点消息广播(PubSub) | OnMessage |

**Priority 选择原则:**

```
安全类插件(黑名单,自动封禁,限流)-> 0-10(最高优先级,最早拦截,减少无效处理)
状态管理类(心跳)                -> 50(依赖连接已通过安全检查)
持久化类                          -> 90(依赖前序插件处理完成)
集群类                            -> 95(最后执行,广播最终 payload)

自定义插件建议:
  安全类  -> 1-9
  业务类  -> 11-49
  监控类  -> 55-65
```

---

> **说明:** 第 4 至 11 节的插件实现是**设计示意片段**,用于说明插件如何组织;
> 实际落地的内置插件只使用两个控制信号 `core.ErrPluginDrop` / `core.ErrPluginBlock`,
> 且不自行写指标(指标录制点见 `docs/guides/Architecture.md` 9.1 节).
> 片段中的缓存/存储调用已按 `internal/infra` 的真实接口(`Cache.Get` 返回 `(val, ok)`,
> `Store.Load` 返回 `(val, found, err)`)校正.

## 4. BlacklistPlugin

### 4.1 设计

```
职责:基于 IP 和 CIDR 段的黑名单过滤
Priority:0(最先执行)
触发:OnAccept(检查来源 IP)
效果:命中黑名单 -> ErrPluginBlock

存储结构:
  exactMap  map[string]time.Time  // 精确 IP -> 过期时间,O(1) 查找
  cidrList  []net.IPNet           // CIDR 段列表,顺序遍历
  cache     infrastructure.Cache  // 分布式黑名单缓存(可选,Miss 降级本地)
```

### 4.2 结构定义

```go
// internal/plugin/blacklist.go

type BlacklistPlugin struct {
    core.BasePlugin

    mu        sync.RWMutex
    exactMap  map[string]time.Time // IP -> 过期时间(零值表示永不过期)
    cidrList  []net.IPNet

    cache   infrastructure.Cache  // 可选,nil 时不使用
    logger  core.Logger
    metrics core.Metrics

    stopCh    chan struct{}
    closeOnce sync.Once
}

var _ core.Plugin = (*BlacklistPlugin)(nil)

func (p *BlacklistPlugin) Name() string     { return "blacklist" }
func (p *BlacklistPlugin) Priority() int    { return 0 }
```

### 4.3 OnAccept 实现

```go
func (p *BlacklistPlugin) OnAccept(sess core.Session) error {
    ip := extractIP(sess.RemoteAddr())
    if ip == "" {
        return nil // 无法解析 IP,放行
    }

    // 步骤1:精确 IP 本地查找(O(1))
    if p.isExactBlacklisted(ip) {
        p.recordBlock(ip, "exact_ip", sess)
        return core.ErrPluginBlock
    }

    // 步骤2:CIDR 段遍历
    if p.isCIDRBlacklisted(ip) {
        p.recordBlock(ip, "cidr", sess)
        return core.ErrPluginBlock
    }

    // 步骤3:分布式缓存查找(若配置了 Cache)
    if p.cache != nil {
        if blocked, _ := p.isCacheBlacklisted(ip); blocked {
            p.recordBlock(ip, "cache", sess)
            return core.ErrPluginBlock
        }
    }

    return nil
}

func (p *BlacklistPlugin) isExactBlacklisted(ip string) bool {
    p.mu.RLock()
    expireAt, exists := p.exactMap[ip]
    p.mu.RUnlock()

    if !exists {
        return false
    }
    // 零值表示永不过期
    if expireAt.IsZero() {
        return true
    }
    // 惰性过期
    if time.Now().After(expireAt) {
        p.mu.Lock()
        delete(p.exactMap, ip)
        p.mu.Unlock()
        return false
    }
    return true
}

func (p *BlacklistPlugin) isCIDRBlacklisted(ip string) bool {
    parsedIP := net.ParseIP(ip)
    if parsedIP == nil {
        return false
    }

    p.mu.RLock()
    defer p.mu.RUnlock()

    for _, cidr := range p.cidrList {
        if cidr.Contains(parsedIP) {
            return true
        }
    }
    return false
}

func (p *BlacklistPlugin) isCacheBlacklisted(ip string) (bool, error) {
    // Cache.Get 的第二个返回值表达命中与否,没有单独的 error 通道
    _, ok := p.cache.Get("blacklist:ip:" + ip)
    return ok, nil // 命中即黑名单;未命中放行
}

func (p *BlacklistPlugin) recordBlock(ip, reason string, sess core.Session) {
    p.metrics.IncCounter("rejected_connections_total", "protocol", sess.Protocol().String(),
        "reason", "blacklisted")
    p.logger.Info("connection blocked by blacklist",
        "ip", ip,
        "reason", reason,
        "session_id", sess.ID())
}
```

### 4.4 动态管理 API

```go
// Add 将 IP 加入黑名单,ttl 为 0 表示永久封禁.
func (p *BlacklistPlugin) Add(ip string, ttl time.Duration) {
    var expireAt time.Time
    if ttl > 0 {
        expireAt = time.Now().Add(ttl)
    }

    p.mu.Lock()
    p.exactMap[ip] = expireAt
    p.mu.Unlock()

    // 同步到分布式缓存(若配置)
    if p.cache != nil {
        ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
        defer cancel()
        p.cache.Set(ctx, "blacklist:ip:"+ip, []byte("1"), ttl)
    }
}

// Remove 从黑名单移除 IP.
func (p *BlacklistPlugin) Remove(ip string) {
    p.mu.Lock()
    delete(p.exactMap, ip)
    p.mu.Unlock()

    if p.cache != nil {
        ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
        defer cancel()
        p.cache.Del(ctx, "blacklist:ip:"+ip)
    }
}

// AddCIDR 将 CIDR 段加入黑名单.
func (p *BlacklistPlugin) AddCIDR(cidr string) error {
    _, network, err := net.ParseCIDR(cidr)
    if err != nil {
        return fmt.Errorf("invalid CIDR %q: %w", cidr, err)
    }

    p.mu.Lock()
    p.cidrList = append(p.cidrList, *network)
    p.mu.Unlock()
    return nil
}

// Reload 重新加载黑名单列表(替换全量数据).
func (p *BlacklistPlugin) Reload(ips []string, cidrs []string) error {
    newExactMap := make(map[string]time.Time, len(ips))
    for _, ip := range ips {
        newExactMap[ip] = time.Time{} // 永久
    }

    newCIDRList := make([]net.IPNet, 0, len(cidrs))
    for _, cidr := range cidrs {
        _, network, err := net.ParseCIDR(cidr)
        if err != nil {
            return fmt.Errorf("invalid CIDR %q: %w", cidr, err)
        }
        newCIDRList = append(newCIDRList, *network)
    }

    p.mu.Lock()
    p.exactMap = newExactMap
    p.cidrList = newCIDRList
    p.mu.Unlock()
    return nil
}
```

### 4.5 后台清理与 OnClose

```go
func (p *BlacklistPlugin) startCleanupLoop() {
    go func() {
        ticker := time.NewTicker(time.Minute)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                p.cleanExpired()
            case <-p.stopCh:
                return
            }
        }
    }()
}

func (p *BlacklistPlugin) cleanExpired() {
    now := time.Now()
    p.mu.Lock()
    for ip, expireAt := range p.exactMap {
        if !expireAt.IsZero() && now.After(expireAt) {
            delete(p.exactMap, ip)
        }
    }
    p.mu.Unlock()
}

func (p *BlacklistPlugin) OnClose(sess core.Session) {
    // BlacklistPlugin 无 per-session 状态,OnClose 无需操作
}

// extractIP 从 net.Addr 提取纯 IP 字符串.
func extractIP(addr net.Addr) string {
    if addr == nil {
        return ""
    }
    host, _, err := net.SplitHostPort(addr.String())
    if err != nil {
        return addr.String()
    }
    return host
}
```

---

## 5. RateLimitPlugin

### 5.1 设计

```
职责:双层令牌桶限流(全局 + per-IP)
Priority:10
触发:
  OnAccept -> 连接速率限流(超限返回 ErrPluginBlock)
  OnMessage -> 消息速率限流(超限返回 ErrPluginDrop)
副作用:连续触发超 N 次 -> 通知 AutoBanPlugin
```

### 5.2 tokenBucket 实现

```go
// internal/plugin/ratelimit.go

type tokenBucket struct {
    rate       float64      // 令牌补充速率(tokens/second)
    burst      float64      // 桶容量(最大令牌数)
    tokens     float64      // 当前令牌数
    lastRefill time.Time    // 上次补充时间
    mu         sync.Mutex
}

func newTokenBucket(rate, burst float64) *tokenBucket {
    return &tokenBucket{
        rate:       rate,
        burst:      burst,
        tokens:     burst, // 初始满桶
        lastRefill: time.Now(),
    }
}

// Allow 尝试消耗一个令牌,返回是否允许.
func (b *tokenBucket) Allow() bool {
    b.mu.Lock()
    defer b.mu.Unlock()

    now := time.Now()
    elapsed := now.Sub(b.lastRefill).Seconds()
    b.lastRefill = now

    // 按时间差补充令牌
    b.tokens += elapsed * b.rate
    if b.tokens > b.burst {
        b.tokens = b.burst
    }

    if b.tokens >= 1.0 {
        b.tokens -= 1.0
        return true
    }
    return false
}

// IsIdle 检查桶是否长时间未使用(用于 cleanupLoop).
func (b *tokenBucket) IsIdle(idleTTL time.Duration) bool {
    b.mu.Lock()
    defer b.mu.Unlock()
    return time.Since(b.lastRefill) > idleTTL
}
```

### 5.3 RateLimitPlugin 结构

```go
type RateLimitPlugin struct {
    core.BasePlugin

    globalBucket  *tokenBucket
    perIPBuckets  sync.Map       // string(IP) -> *tokenBucket
    options       RateLimitOptions

    // AutoBan 通知(计数器,超阈值时由 AutoBanPlugin 读取)
    perIPViolations sync.Map     // string(IP) -> *atomic.Int64

    logger  core.Logger
    metrics core.Metrics

    stopCh    chan struct{}
    closeOnce sync.Once
}

var _ core.Plugin = (*RateLimitPlugin)(nil)

func (p *RateLimitPlugin) Name() string  { return "ratelimit" }
func (p *RateLimitPlugin) Priority() int { return 10 }
```

### 5.4 OnAccept 与 OnMessage 实现

```go
func (p *RateLimitPlugin) OnAccept(sess core.Session) error {
    // 全局连接限流
    if !p.globalBucket.Allow() {
        p.metrics.IncCounter("rejected_connections_total", "protocol", sess.Protocol().String(),
            "reason", "global_rate_limited")
        return fmt.Errorf("%w: global connection rate exceeded",
            core.ErrPluginBlock)
    }

    // per-IP 连接限流
    ip := extractIP(sess.RemoteAddr())
    bucket := p.getOrCreateIPBucket(ip)
    if !bucket.Allow() {
        p.incrementViolation(ip)
        p.metrics.IncCounter("rejected_connections_total", "protocol", sess.Protocol().String(),
            "reason", "ip_rate_limited")
        p.logger.Warn("connection rate limited",
            "ip", ip,
            "session_id", sess.ID())
        return fmt.Errorf("%w: per-IP connection rate exceeded for %s",
            core.ErrPluginBlock, ip)
    }

    return nil
}

func (p *RateLimitPlugin) OnMessage(sess core.Session, data []byte) ([]byte, error) {
    ip := extractIP(sess.RemoteAddr())
    bucket := p.getOrCreateIPBucket(ip)

    if !bucket.Allow() {
        p.incrementViolation(ip)
        p.metrics.IncCounter("dropped_messages_total", "protocol", sess.Protocol().String(),
            "reason", "message_rate_exceeded")
        return nil, fmt.Errorf("%w: message rate exceeded for %s",
            core.ErrPluginDrop, ip)
    }

    return data, nil
}

func (p *RateLimitPlugin) getOrCreateIPBucket(ip string) *tokenBucket {
    if val, ok := p.perIPBuckets.Load(ip); ok {
        return val.(*tokenBucket)
    }
    newBucket := newTokenBucket(p.options.PerIPRate, p.options.PerIPBurst)
    actual, _ := p.perIPBuckets.LoadOrStore(ip, newBucket)
    return actual.(*tokenBucket)
}

func (p *RateLimitPlugin) incrementViolation(ip string) {
    val, _ := p.perIPViolations.LoadOrStore(ip, &atomic.Int64{})
    count := val.(*atomic.Int64).Add(1)

    if count >= int64(p.options.ViolationThreshold) {
        p.logger.Warn("rate limit violation threshold reached",
            "ip", ip,
            "count", count,
            "threshold", p.options.ViolationThreshold)
        // AutoBanPlugin 通过 ViolationCount(ip) 方法读取此计数
    }
}

// ViolationCount 返回指定 IP 的累计违规次数(供 AutoBanPlugin 查询).
func (p *RateLimitPlugin) ViolationCount(ip string) int64 {
    if val, ok := p.perIPViolations.Load(ip); ok {
        return val.(*atomic.Int64).Load()
    }
    return 0
}

// ResetViolation 重置指定 IP 的违规计数(AutoBanPlugin 封禁后调用).
func (p *RateLimitPlugin) ResetViolation(ip string) {
    p.perIPViolations.Delete(ip)
}
```

### 5.5 cleanupLoop

```go
func (p *RateLimitPlugin) startCleanupLoop() {
    go func() {
        ticker := time.NewTicker(2 * time.Minute)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                p.cleanIdleBuckets()
            case <-p.stopCh:
                return
            }
        }
    }()
}

func (p *RateLimitPlugin) cleanIdleBuckets() {
    cleaned := 0
    p.perIPBuckets.Range(func(key, val any) bool {
        bucket := val.(*tokenBucket)
        if bucket.IsIdle(2 * time.Minute) {
            // Go 1.26 sync.Map.CompareAndDelete
            if p.perIPBuckets.CompareAndDelete(key, val) {
                p.perIPViolations.Delete(key)
                cleaned++
            }
        }
        return true
    })
    if cleaned > 0 {
        p.logger.Debug("rate limit: cleaned idle IP buckets",
            "count", cleaned)
    }
}
```

---

## 6. AutoBanPlugin

### 6.1 设计

```
职责:监控违规行为,超阈值后自动加入黑名单
Priority:5(紧接 BlacklistPlugin,在 RateLimitPlugin 之前)
触发:
  OnAccept -> 检查协议错误次数
  OnMessage -> 检查消息级违规
  内部定时检查 RateLimitPlugin 违规计数

触发条件(可配置):
  单 IP 限流违规超 N 次(默认 10 次)
  单 IP 协议错误超 M 次(默认 5 次)
  单 IP 空连接超 K 次(默认 20 次,防 Slowloris)

动作:调用 BlacklistPlugin.Add(ip, banTTL)
banTTL:默认 30 分钟
```

### 6.2 结构定义

```go
// internal/plugin/autoban.go

type AutoBanPlugin struct {
    core.BasePlugin

    blacklist  *BlacklistPlugin
    ratelimit  *RateLimitPlugin

    // 协议错误计数(IP -> count)
    protocolErrors sync.Map // string(IP) -> *atomic.Int64

    options AutoBanOptions
    logger  core.Logger
    metrics core.Metrics

    stopCh    chan struct{}
    closeOnce sync.Once
}

var _ core.Plugin = (*AutoBanPlugin)(nil)

func (p *AutoBanPlugin) Name() string  { return "autoban" }
func (p *AutoBanPlugin) Priority() int { return 5 }
```

### 6.3 OnAccept 与 OnMessage 实现

```go
func (p *AutoBanPlugin) OnAccept(sess core.Session) error {
    ip := extractIP(sess.RemoteAddr())
    if ip == "" {
        return nil
    }

    // 检查限流违规次数
    if p.ratelimit != nil {
        violations := p.ratelimit.ViolationCount(ip)
        if violations >= int64(p.options.RateLimitViolationThreshold) {
            p.ban(ip, "rate_limit_violations",
                fmt.Sprintf("violations=%d", violations))
            return fmt.Errorf("%w: auto banned due to rate limit violations",
                core.ErrAutoBanned)
        }
    }

    return nil
}

func (p *AutoBanPlugin) OnMessage(sess core.Session, data []byte) ([]byte, error) {
    // AutoBanPlugin 不修改消息内容,透传
    return data, nil
}

// RecordProtocolError 记录协议错误(由协议层调用).
func (p *AutoBanPlugin) RecordProtocolError(ip string) {
    val, _ := p.protocolErrors.LoadOrStore(ip, &atomic.Int64{})
    count := val.(*atomic.Int64).Add(1)

    if count >= int64(p.options.ProtocolErrorThreshold) {
        p.ban(ip, "protocol_errors",
            fmt.Sprintf("errors=%d", count))
    }
}

func (p *AutoBanPlugin) ban(ip, reason, detail string) {
    if p.blacklist == nil {
        return
    }

    p.blacklist.Add(ip, p.options.BanTTL)

    // 重置违规计数
    if p.ratelimit != nil {
        p.ratelimit.ResetViolation(ip)
    }
    p.protocolErrors.Delete(ip)

    p.metrics.IncCounter("autoban_total", "reason", reason)
    p.logger.Warn("ip auto banned",
        "ip", ip,
        "reason", reason,
        "detail", detail,
        "ban_ttl", p.options.BanTTL)
}
```

---

## 7. HeartbeatPlugin

### 7.1 设计

```
职责:检测空闲超时,关闭不活跃会话
Priority:50
触发:
  OnAccept -> 记录会话(P2:注册到时间轮)
  OnMessage -> 重置心跳计时器(TouchActive 已在协议层调用,此处可跳过)
  后台扫描 -> 检测 LastActiveAt 超过 IdleTimeout

P0 实现:ticker 定期扫描 SessionManager
P2 演进:时间轮(benchmark 证明扫描成为瓶颈后引入)
```

### 7.2 P0 实现(ticker 扫描)

```go
// internal/plugin/heartbeat.go

type HeartbeatPlugin struct {
    core.BasePlugin

    options HeartbeatOptions
    manager core.SessionManager

    logger  core.Logger
    metrics core.Metrics

    stopCh    chan struct{}
    closeOnce sync.Once
}

var _ core.Plugin = (*HeartbeatPlugin)(nil)

func (p *HeartbeatPlugin) Name() string  { return "heartbeat" }
func (p *HeartbeatPlugin) Priority() int { return 50 }

func (p *HeartbeatPlugin) OnAccept(sess core.Session) error {
    // P0:无需 per-session 注册,扫描时遍历 Manager
    // P2:注册到时间轮
    return nil
}

func (p *HeartbeatPlugin) OnMessage(sess core.Session, data []byte) ([]byte, error) {
    // TouchActive 已由协议层的 readLoop 调用(sess.TouchActive())
    // 此处无需重复调用
    return data, nil
}

func (p *HeartbeatPlugin) OnClose(sess core.Session) {
    // P2:从时间轮移除
}

func (p *HeartbeatPlugin) start() {
    go func() {
        ticker := time.NewTicker(p.options.CheckInterval)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                p.scan()
            case <-p.stopCh:
                return
            }
        }
    }()
}

func (p *HeartbeatPlugin) scan() {
    if p.manager == nil {
        return
    }

    now := time.Now()
    expired := 0

    p.manager.Range(func(sess core.Session) bool {
        if !sess.IsAlive() {
            return true
        }
        if now.Sub(sess.LastActiveAt()) > p.options.IdleTimeout {
            p.logger.Info("session idle timeout, closing",
                "session_id", sess.ID(),
                "protocol", sess.Protocol(),
                "last_active", sess.LastActiveAt(),
                "idle_timeout", p.options.IdleTimeout)
            p.metrics.IncCounter("session_errors_total", "protocol", sess.Protocol().String(),
                "reason", "idle_timeout")
            go sess.Close(context.Background())
            expired++
        }
        return true
    })

    if expired > 0 {
        p.logger.Debug("heartbeat scan completed",
            "expired", expired)
    }
}
```

### 7.3 P2 时间轮方案(占位说明)

```
触发条件:
  benchmark 证明 HeartbeatPlugin scan() 成为 CPU 热点
  (10 万连接,每秒扫描一次,O(N) 遍历约 1ms)

P2 方案:
  使用 internal/infra/timewheel/timewheel.go(尚未实现)
  OnAccept -> timeWheel.Add(sess.ID(), idleTimeout, callback)
  OnMessage -> timeWheel.Reset(sess.ID())
  OnClose -> timeWheel.Remove(sess.ID())
  超时回调 -> sess.Close()

优势:
  10 万连接仅 1 个系统 goroutine 管理所有定时器
  Add/Reset/Remove 均 O(1)
  替换时不改变 Plugin 接口,只改 HeartbeatPlugin 内部实现
```

---

## 8. PersistencePlugin

### 8.1 设计

```
职责:会话消息异步持久化
Priority:90
触发:
  OnAccept -> 加载历史数据到 Session Meta
  OnMessage -> 异步写入 Store
  OnClose -> 同步写入最终快照

关键设计:
  有界 channel(writeCh,容量 1024)防止内存积压
  批量写入(每 100 条或 500ms 刷一次)减少 Store 调用
  CircuitBreaker 包裹 Store 调用,Store 不可用时跳过
  sync.Once 保证 OnClose 最终快照只写一次
```

### 8.2 结构定义

```go
// internal/plugin/persistence.go

type writeEntry struct {
    sessionID uint64
    data      []byte
}

type PersistencePlugin struct {
    core.BasePlugin

    store    infrastructure.Store
    breaker  *infrastructure.CircuitBreaker

    writeCh   chan writeEntry
    options   PersistenceOptions
    logger    core.Logger
    metrics   core.Metrics

    // per-session 关闭保护
    closedSessions sync.Map // uint64 -> sync.Once

    stopCh    chan struct{}
    closeOnce sync.Once
    wg        sync.WaitGroup
}

var _ core.Plugin = (*PersistencePlugin)(nil)

func (p *PersistencePlugin) Name() string  { return "persistence" }
func (p *PersistencePlugin) Priority() int { return 90 }
```

### 8.3 OnAccept 实现

```go
func (p *PersistencePlugin) OnAccept(sess core.Session) error {
    ctx, cancel := context.WithTimeout(
        context.Background(), p.options.StoreTimeout)
    defer cancel()

    // 尝试加载历史数据
    err := p.breaker.Do(func() error {
        data, found, err := p.store.Load(p.bucket, sessionStoreKey(sess.ID()))
        if err != nil {
            return err
        }
        if !found {
            return nil // 新会话,无历史数据
        }
        sess.SetMeta("persistence:history", data)
        return nil
    })

    if err != nil {
        if errors.Is(err, core.ErrCircuitOpen) {
            p.logger.Warn("persistence: circuit open, skip load history",
                "session_id", sess.ID())
            return nil // 降级:跳过历史加载,不阻塞连接
        }
        p.logger.Warn("persistence: load history failed",
            "session_id", sess.ID(),
            "error", err)
    }

    return nil
}
```

### 8.4 OnMessage 实现

```go
func (p *PersistencePlugin) OnMessage(
    sess core.Session, data []byte,
) ([]byte, error) {
    // 序列化消息(当前使用 JSON,可替换 protobuf / msgpack)
    entry := writeEntry{
        sessionID: sess.ID(),
        data:      data,
    }

    // 异步写入(非阻塞)
    select {
    case p.writeCh <- entry:
        // 成功入队
    default:
        // 队列满,丢弃(记录指标)
        p.metrics.IncCounter("dropped_messages_total", "protocol", sess.Protocol().String(),
            "reason", "persistence_queue_full")
        p.logger.Warn("persistence: write queue full, dropping message",
            "session_id", sess.ID())
    }

    return data, nil // 不修改消息内容
}
```

### 8.5 OnClose 实现

```go
func (p *PersistencePlugin) OnClose(sess core.Session) {
    // 获取或创建 per-session Once
    val, _ := p.closedSessions.LoadOrStore(sess.ID(), &sync.Once{})
    once := val.(*sync.Once)

    once.Do(func() {
        defer p.closedSessions.Delete(sess.ID())

        ctx, cancel := context.WithTimeout(
            context.Background(), p.options.StoreTimeout)
        defer cancel()

        // 同步写入最终快照
        snapshot := buildSnapshot(sess)
        err := p.breaker.Do(func() error {
            return p.store.Save(ctx, sessionStoreKey(sess.ID()), snapshot)
        })
        if err != nil {
            if !errors.Is(err, core.ErrCircuitOpen) {
                p.logger.Warn("persistence: save final snapshot failed",
                    "session_id", sess.ID(),
                    "error", err)
            }
        }
    })
}

func sessionStoreKey(sessionID uint64) string {
    return fmt.Sprintf("session:%d:state", sessionID)
}

func buildSnapshot(sess core.Session) []byte {
    snapshot := map[string]any{
        "session_id":  sess.ID(),
        "protocol":    sess.Protocol().String(),
        "remote_addr": sess.RemoteAddr().String(),
        "created_at":  sess.CreatedAt().Unix(),
        "closed_at":   time.Now().Unix(),
    }
    data, _ := json.Marshal(snapshot)
    return data
}
```

### 8.6 batchWriter 实现

```go
func (p *PersistencePlugin) batchWriter() {
    defer p.wg.Done()

    batch := make([]writeEntry, 0, p.options.BatchSize)
    ticker := time.NewTicker(p.options.FlushInterval)
    defer ticker.Stop()

    flush := func() {
        if len(batch) == 0 {
            return
        }

        ctx, cancel := context.WithTimeout(
            context.Background(), p.options.StoreTimeout)
        defer cancel()

        for _, entry := range batch {
            err := p.breaker.Do(func() error {
                return p.store.Save(ctx,
                    sessionStoreKey(entry.sessionID), entry.data)
            })
            if err != nil {
                if errors.Is(err, core.ErrCircuitOpen) {
                    p.logger.Warn("persistence: circuit open, skip batch flush")
                    break // 熔断时停止本批次写入
                }
                p.logger.Warn("persistence: batch save failed",
                    "error", err)
            }
        }
        batch = batch[:0]
    }

    for {
        select {
        case entry := <-p.writeCh:
            batch = append(batch, entry)
            if len(batch) >= p.options.BatchSize {
                flush()
            }

        case <-ticker.C:
            flush()

        case <-p.stopCh:
            // 排空 writeCh
            for {
                select {
                case entry := <-p.writeCh:
                    batch = append(batch, entry)
                default:
                    flush()
                    return
                }
            }
        }
    }
}
```

---

## 9. Persistence Plugin

> Store + MessageLog durable persistence with error returns.

### 9.1 OnMessage Hook
```go
func (p *Persistence) OnMessage(sess core.Session, data []byte) ([]byte, error) {
    if p.MessageLog() != nil {
        p.MessageLog().Append(data)
    }
    return data, nil  // transmit data even if log fails
}
```

### 9.2 Usage
```go
store, _ := NewBoltStore("/data/shark.db")
p := NewPersistence(store, "sessions")
log := p.MessageLog()
log.Replay(func(seq uint64, data []byte) error { return nil })
log.Prune(1000)
```

---

## 10. ClusterPlugin

### 10.1 设计

```
职责:跨节点会话路由 + 集群事件广播
Priority:95
触发:
  OnAccept -> 写入会话路由到 Cache + 发布 joined 事件
  OnClose -> 删除路由 + 发布 left 事件
  后台 -> 订阅路由消息 + 节点心跳

跨节点路由流程:
  本节点 Manager.Get(targetID) -> 未找到
  -> Cache.Get("session:route:"+targetID) -> 远端 nodeID
  -> PubSub.Publish("node."+nodeID+".route", payload)
  -> 远端节点 ClusterPlugin 订阅,转发到本地 Session
```

### 10.2 结构定义

```go
// internal/plugin/cluster.go

type ClusterPlugin struct {
    core.BasePlugin

    nodeID  string
    manager core.SessionManager
    cache   infrastructure.Cache
    pubsub  infrastructure.PubSub
    breaker *infrastructure.CircuitBreaker

    // 路由订阅
    routeSub     infrastructure.Subscription
    options      ClusterOptions
    logger       core.Logger
    metrics      core.Metrics

    stopCh       chan struct{}
    closeOnce    sync.Once
    wg           sync.WaitGroup
}

var _ core.Plugin = (*ClusterPlugin)(nil)

func (p *ClusterPlugin) Name() string  { return "cluster" }
func (p *ClusterPlugin) Priority() int { return 95 }
```

### 10.3 OnAccept 与 OnClose 实现

```go
func (p *ClusterPlugin) OnAccept(sess core.Session) error {
    ctx, cancel := context.WithTimeout(
        context.Background(), p.options.OperationTimeout)
    defer cancel()

    sessID := fmt.Sprintf("%d", sess.ID())

    // 写入会话路由(此节点拥有此会话)
    _ = p.breaker.Do(func() error {
        return p.cache.Set(ctx,
            "session:route:"+sessID,
            []byte(p.nodeID),
            p.options.SessionTTL)
    })

    // 发布 joined 事件
    event := buildClusterEvent("session.joined", sess.ID(), p.nodeID)
    _ = p.breaker.Do(func() error {
        return p.pubsub.Publish(ctx, "cluster.events", event)
    })

    return nil
}

func (p *ClusterPlugin) OnClose(sess core.Session) {
    ctx, cancel := context.WithTimeout(
        context.Background(), p.options.OperationTimeout)
    defer cancel()

    sessID := fmt.Sprintf("%d", sess.ID())

    _ = p.breaker.Do(func() error {
        return p.cache.Del(ctx, "session:route:"+sessID)
    })

    event := buildClusterEvent("session.left", sess.ID(), p.nodeID)
    _ = p.breaker.Do(func() error {
        return p.pubsub.Publish(ctx, "cluster.events", event)
    })
}
```

### 10.4 路由转发

```go
// Route 向目标 Session 转发消息(跨节点感知).
// 由业务 Handler 调用,不在插件链中执行.
func (p *ClusterPlugin) Route(
    ctx context.Context,
    targetSessionID uint64,
    data []byte,
) error {
    // 本地查找
    if sess, ok := p.manager.Get(targetSessionID); ok {
        return sess.Send(data)
    }

    // 查询远端节点
    sessIDStr := fmt.Sprintf("%d", targetSessionID)
    nodeIDBytes, ok := p.cache.Get("session:route:" + sessIDStr)
    if !ok {
        return fmt.Errorf("%w: session %d not found in cluster",
            core.ErrSessionClosed, targetSessionID)
    }

    remoteNodeID := string(nodeIDBytes)
    if remoteNodeID == p.nodeID {
        // 路由指向本节点但 Manager 未找到(会话已关闭)
        return fmt.Errorf("%w: session %d routing inconsistency",
            core.ErrSessionClosed, targetSessionID)
    }

    // 向远端节点发布路由消息
    payload := buildRoutePayload(targetSessionID, data)
    return p.pubsub.Publish(ctx, "node."+remoteNodeID+".route", payload)
}

func (p *ClusterPlugin) startRouteSubscription() error {
    ctx := context.Background()
    sub, err := p.pubsub.Subscribe(ctx,
        "node."+p.nodeID+".route",
        p.handleRouteMessage)
    if err != nil {
        return err
    }
    p.routeSub = sub
    return nil
}

func (p *ClusterPlugin) handleRouteMessage(data []byte) {
    targetID, payload, err := parseRoutePayload(data)
    if err != nil {
        p.logger.Error("cluster: invalid route payload", "error", err)
        return
    }

    sess, ok := p.manager.Get(targetID)
    if !ok {
        p.logger.Warn("cluster: route target not found",
            "target_session_id", targetID)
        return
    }

    if err := sess.Send(payload); err != nil {
        p.logger.Error("cluster: route send failed",
            "target_session_id", targetID,
            "error", err)
    }
}
```

### 10.5 节点心跳

```go
func (p *ClusterPlugin) startHeartbeat() {
    p.wg.Add(1)
    go func() {
        defer p.wg.Done()
        interval := p.options.HeartbeatTTL / 2
        ticker := time.NewTicker(interval)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                ctx, cancel := context.WithTimeout(
                    context.Background(), p.options.OperationTimeout)
                _ = p.breaker.Do(func() error {
                    return p.cache.Set(ctx,
                        "node:"+p.nodeID,
                        []byte(time.Now().Format(time.RFC3339)),
                        p.options.HeartbeatTTL)
                })
                cancel()
            case <-p.stopCh:
                return
            }
        }
    }()
}
```

---

## 11. SlowHandlerPlugin

### 11.1 设计

```
职责:记录执行时间超过阈值的 Handler 调用
Priority:60(最后执行,观察最终 payload)
触发:OnMessage(包装计时,不修改消息内容)
用途:定位业务 Handler 性能问题,不影响主流程
```

### 11.2 实现

```go
// internal/plugin/slowhandler.go

type SlowHandlerPlugin struct {
    core.BasePlugin

    threshold time.Duration // 慢处理阈值(默认 100ms)
    logger    core.Logger
    metrics   core.Metrics
}

var _ core.Plugin = (*SlowHandlerPlugin)(nil)

func (p *SlowHandlerPlugin) Name() string  { return "slowhandler" }
func (p *SlowHandlerPlugin) Priority() int { return 60 }

func (p *SlowHandlerPlugin) OnMessage(
    sess core.Session, data []byte,
) ([]byte, error) {
    // SlowHandlerPlugin 在插件链中只记录时间戳
    // 实际 Handler 执行时间由 WorkerPool 的 runTask 记录
    // 此处记录消息进入插件链的时间,供后续分析
    sess.SetMeta("slowhandler:entry_time", time.Now())
    return data, nil
}

// RecordHandlerDuration 由 WorkerPool 在 Handler 执行后调用.
func (p *SlowHandlerPlugin) RecordHandlerDuration(
    sess core.Session, duration time.Duration,
) {
    if duration <= p.threshold {
        return
    }

    p.metrics.IncCounter("handler_duration_seconds", "protocol", sess.Protocol().String())
    p.logger.Warn("slow handler detected",
        "session_id", sess.ID(),
        "protocol", sess.Protocol(),
        "duration_ms", duration.Milliseconds(),
        "threshold_ms", p.threshold.Milliseconds(),
        "remote_addr", sess.RemoteAddr().String())
}
```

---

## 12. 自定义插件指南

### 12.1 最小实现模板

```go
// 自定义插件:只需嵌入 BasePlugin,覆盖关心的方法
type MyPlugin struct {
    core.BasePlugin
    // 自定义字段
}

var _ core.Plugin = (*MyPlugin)(nil)

func (p *MyPlugin) Name() string  { return "my-plugin" }
func (p *MyPlugin) Priority() int { return 35 } // 在限流(10)和心跳(50)之后

// 只覆盖需要的方法
func (p *MyPlugin) OnMessage(
    sess core.Session, data []byte,
) ([]byte, error) {
    // 业务逻辑
    // 不修改消息:return data, nil
    // 丢弃消息:return nil, core.ErrPluginDrop
    // 关闭连接:return nil, core.ErrPluginBlock
    return data, nil
}
```

### 12.2 注册到插件链

插件在构造 Gateway 时一次性挂载:

```go
gw := api.NewGateway(api.WithPlugins(
    api.NewBlacklistPlugin(),
    api.NewRateLimitPlugin(10000, time.Second),
    &MyPlugin{},
))
```

`WithPlugins` 内部构造 `runtime.NewPluginChain(plugins...)`,链内按 `Priority` 稳定排序.
需要在运行期追加时,通过 `Append` 在链上追加(链是 copy-on-write 的,`Append` 与
并发回调安全).`internal/runtime` 是模块内部包,该写法只在模块内可用:

```go
if chain, ok := gw.Runtime().Plugins().(*runtime.PluginChain); ok {
    chain.Append(&MyPlugin{})
}
```

`Append` 是具体类型 `PluginChain` 的方法,不在 `core.PluginRunner` 接口内:接口只描述
"如何执行插件",注册属于装配阶段的具体实现细节.

### 12.3 自定义插件检查清单

```
功能验证:
  [ ] Name() 返回可辨识名称(链不去重,同名插件会共存并都被执行)
  [ ] Priority() 不与内置插件冲突(参考第 3 节 Priority 表)
  [ ] OnAccept 返回 ErrPluginBlock 时传输层会拒绝并关闭连接
  [ ] OnMessage 返回 ErrPluginDrop 时消息被丢弃,连接继续
  [ ] OnClose 不返回错误(void),即使失败也不影响其他插件
  [ ] 不需要自行 recover:PluginChain 的 safe* 包装会捕获 panic
      并转为 ErrPluginPanic(Start/Stop 同样被包装)

资源管理:
  [ ] 有状态插件(goroutine,ticker)使用 sync.Once 保证幂等关闭
  [ ] 实现 core.LifecyclePlugin(Start/Stop)的插件要能被重复 Start/Stop
  [ ] Start 失败时不留后台 goroutine(链会回滚,但回滚只调 Stop)
  [ ] 不持有 Session 的强引用(避免内存泄漏)
  [ ] 不在 OnClose 中阻塞(OnClose 逆序串行执行,阻塞影响整体关闭速度)

并发安全:
  [ ] 共享状态使用 sync.RWMutex 或 sync.Map
  [ ] 计数器使用 atomic.Int64
  [ ] 不在回调内依赖 PluginChain 的锁语义:回调在锁外执行,插件自身状态需自保

指标:
  [ ] 使用 core.Metrics 接口,不直接引用 Prometheus
  [ ] 指标名称使用 <plugin>_<action>_total 形式,并避免无界标签
```

---

## 附:指标录制 API 约定

框架的 `core.Metrics` 只有三个方法,没有 `Counter()/.Inc()` 之类的链式工厂:

```go
IncCounter(name string, labels ...string)
SetGauge(name string, value float64, labels ...string)
ObserveHistogram(name string, value float64, labels ...string)
```

`labels` 以 key/value 交替传入.本文档中的指标名是**示意名**,用于说明接入方式的形状;
框架自身实际录制的指标只有 5 个(`sessions_accepted_total` / `sessions_closed_total` /
`sessions_active` / `sessions_reclaimed_total` / `tcp_task_queue_full_total`),
清单见 `docs/guides/Architecture.md` 9.1 节.实现参考
`internal/infra/observability/prometheus.go`.
