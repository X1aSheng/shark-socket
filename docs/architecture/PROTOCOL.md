# PROTOCOL.md

> Shark-Socket 应用协议层：LwM2M 详细设计  
> 版本：v0.2.x-alpha  
> 最后更新：2026-06-01

---

## 目录

1. [LwM2M 概述](#1-lwm2m-概述)
2. [数据模型](#2-数据模型)
3. [LwM2M Server](#3-lwm2m-server)
4. [LwM2M Client](#4-lwm2m-client)
5. [CoAP 接入层](#5-coap-接入层)
6. [配置项完整参考](#6-配置项完整参考)

---

## 1. LwM2M 概述

### 1.1 在框架中的定位

LwM2M（Lightweight Machine to Machine）是 OMA 制定的**IoT 设备管理协议**，基于 CoAP transport。

**层次关系：**

```
internal/protocol/lwm2m/     ← 应用协议层（本文档）
  提供：设备注册、资源读写、执行命令
  基于：internal/transport/coap/（帧解析、CON/ACK、去重）
  不重复：CoAP 帧结构、retransmitLoop、MessageID 去重
```

**与 CoAP transport 的集成边界（ADR-009）：**

| 边界 | CoAP transport 负责 | LwM2M protocol 负责 |
|------|-------------------|-------------------|
| 帧解析 | ✓（Parse / Marshal） | ✗ |
| CON/ACK 可靠性 | ✓（retransmitLoop） | ✗ |
| MessageID 去重 | ✓（messageCache） | ✗ |
| Uri-Path 解析 | 提供辅助函数 | ✓（解释语义） |
| 注册生命周期 | ✗ | ✓ |
| 资源模型 | ✗ | ✓ |
| 设备管理命令 | ✗ | ✓ |

### 1.2 P0 实现范围

| 功能 | P0（当前） | P1（后续） |
|------|-----------|-----------|
| 注册 / 更新 / 注销 | ✓ | - |
| Resource Read / Write / Execute | ✓ | - |
| 对象路径解析（/OID/IID/RID） | ✓ | - |
| Link-Format 序列化 | ✓ | - |
| 生命周期 sweep | ✓ | - |
| Observe / Notify | ✗ | ✓ |
| Bootstrap | ✗ | ✓ |
| TLV / SenML 编码 | ✗ | ✓ |
| DTLS | ✗ | ✓ |

### 1.3 文件清单

```
internal/protocol/lwm2m/
├── model.go       # ObjectPath / ObjectLink / Resource / Registration
├── constants.go   # Content-Format 常量
├── responder.go   # CoAP 文本命令 responder
├── client.go      # LwM2M Client：Register / Update / Deregister
├── server.go      # LwM2M Server：注册表 + Read / Write / Execute
└── options.go     # Client / Server Functional Options
```

---

## 2. 数据模型

### 2.1 ObjectPath（资源路径）

```go
// internal/protocol/lwm2m/model.go

// ObjectPath 表示 LwM2M 资源路径 /OID/IID/RID。
// OID：Object ID（对象类型，如 3=Device、5=Firmware Update）
// IID：Object Instance ID（对象实例，同类型设备可有多个实例）
// RID：Resource ID（资源，如 3/0/0=Manufacturer）
type ObjectPath struct {
    ObjectID   uint16 // 必须 >= 0
    InstanceID uint16 // 可选（-1 表示未指定）
    ResourceID uint16 // 可选（-1 表示未指定）
    HasInstance bool  // InstanceID 是否有效
    HasResource bool  // ResourceID 是否有效
}

// ParsePath 解析路径字符串为 ObjectPath。
// 支持格式：/OID、/OID/IID、/OID/IID/RID
func ParsePath(path string) (ObjectPath, error) {
    path = strings.TrimPrefix(path, "/")
    parts := strings.Split(path, "/")

    if len(parts) == 0 || len(parts) > 3 {
        return ObjectPath{}, fmt.Errorf(
            "%w: invalid path format %q (expected /OID, /OID/IID, or /OID/IID/RID)",
            core.ErrInvalidMessage, path)
    }

    objectID, err := parseUint16(parts[0])
    if err != nil {
        return ObjectPath{}, fmt.Errorf(
            "%w: invalid OID %q: %v", core.ErrInvalidMessage, parts[0], err)
    }

    result := ObjectPath{ObjectID: objectID}

    if len(parts) >= 2 {
        instanceID, err := parseUint16(parts[1])
        if err != nil {
            return ObjectPath{}, fmt.Errorf(
                "%w: invalid IID %q: %v", core.ErrInvalidMessage, parts[1], err)
        }
        result.InstanceID = instanceID
        result.HasInstance = true
    }

    if len(parts) == 3 {
        resourceID, err := parseUint16(parts[2])
        if err != nil {
            return ObjectPath{}, fmt.Errorf(
                "%w: invalid RID %q: %v", core.ErrInvalidMessage, parts[2], err)
        }
        result.ResourceID = resourceID
        result.HasResource = true
    }

    return result, nil
}

// String 返回路径字符串表示。
func (p ObjectPath) String() string {
    if !p.HasInstance {
        return fmt.Sprintf("/%d", p.ObjectID)
    }
    if !p.HasResource {
        return fmt.Sprintf("/%d/%d", p.ObjectID, p.InstanceID)
    }
    return fmt.Sprintf("/%d/%d/%d", p.ObjectID, p.InstanceID, p.ResourceID)
}

func parseUint16(s string) (uint16, error) {
    n, err := strconv.ParseUint(s, 10, 16)
    if err != nil {
        return 0, err
    }
    return uint16(n), nil
}
```

### 2.2 ObjectLink（Link-Format 条目）

```go
// ObjectLink 表示 Link-Format 中的一个对象链接条目。
// 用于注册请求的 payload（application/link-format）。
// 示例：</3/0>,</5/0>,</1/0>
type ObjectLink struct {
    Path       ObjectPath
    Attributes map[string]string // 可选属性（如 rt="oma.lwm2m"）
}

// FormatLinkFormat 将对象链接列表序列化为 Link-Format 字符串。
// 输出属性顺序固定（按 key 字母排序），保证测试可重复。
func FormatLinkFormat(links []ObjectLink) string {
    if len(links) == 0 {
        return ""
    }

    parts := make([]string, 0, len(links))
    for _, link := range links {
        part := "<" + link.Path.String() + ">"
        if len(link.Attributes) > 0 {
            // 按 key 排序，保证输出稳定
            keys := make([]string, 0, len(link.Attributes))
            for k := range link.Attributes {
                keys = append(keys, k)
            }
            sort.Strings(keys)
            for _, k := range keys {
                part += ";" + k + "=\"" + link.Attributes[k] + "\""
            }
        }
        parts = append(parts, part)
    }
    return strings.Join(parts, ",")
}

// ParseLinkFormat 解析 Link-Format 字符串为对象链接列表。
func ParseLinkFormat(linkFormat string) ([]ObjectLink, error) {
    if linkFormat == "" {
        return nil, nil
    }

    var links []ObjectLink
    entries := strings.Split(linkFormat, ",")

    for _, entry := range entries {
        entry = strings.TrimSpace(entry)
        if entry == "" {
            continue
        }

        link, err := parseLinkEntry(entry)
        if err != nil {
            return nil, err
        }
        links = append(links, link)
    }

    return links, nil
}

func parseLinkEntry(entry string) (ObjectLink, error) {
    // 提取路径 <path>
    if !strings.HasPrefix(entry, "<") {
        return ObjectLink{}, fmt.Errorf(
            "%w: link entry must start with '<': %q",
            core.ErrInvalidMessage, entry)
    }

    closeIdx := strings.Index(entry, ">")
    if closeIdx < 0 {
        return ObjectLink{}, fmt.Errorf(
            "%w: link entry missing '>': %q",
            core.ErrInvalidMessage, entry)
    }

    pathStr := entry[1:closeIdx]
    path, err := ParsePath(pathStr)
    if err != nil {
        return ObjectLink{}, err
    }

    link := ObjectLink{
        Path:       path,
        Attributes: make(map[string]string),
    }

    // 解析属性（;key="value" 格式）
    if closeIdx+1 < len(entry) {
        attrStr := entry[closeIdx+1:]
        attrs := strings.Split(attrStr, ";")
        for _, attr := range attrs {
            attr = strings.TrimSpace(attr)
            if attr == "" {
                continue
            }
            parts := strings.SplitN(attr, "=", 2)
            if len(parts) == 2 {
                key := strings.TrimSpace(parts[0])
                value := strings.Trim(strings.TrimSpace(parts[1]), "\"")
                link.Attributes[key] = value
            }
        }
    }

    return link, nil
}
```

### 2.3 Resource（资源定义）

```go
// Resource 表示 LwM2M 对象实例中的一个资源。
type Resource struct {
    ID    uint16      // Resource ID
    Value []byte      // 当前值（深拷贝，防止外部修改）
    // 回调（由 Client 注册，Server 通过 responder 调用）
    OnRead    func() ([]byte, error)                  // 读取回调
    OnWrite   func(value []byte) error                // 写入回调
    OnExecute func(args []byte) error                 // 执行回调
}

// NewResource 构造 Resource，深拷贝 value 防止外部切片复用。
func NewResource(id uint16, value []byte) Resource {
    copied := make([]byte, len(value))
    copy(copied, value)
    return Resource{ID: id, Value: copied}
}
```

### 2.4 Registration（注册记录）

```go
// Registration 表示一个已注册的 LwM2M Client 记录。
type Registration struct {
    Endpoint    string        // 设备端点名称（唯一标识）
    LocationPath string       // 服务端分配的注册路径（如 /rd/abc123）
    Lifetime    int           // 注册有效期（秒）
    BindingMode string        // 绑定模式（"U"=UDP，"T"=TCP）
    Objects     []ObjectLink  // 设备支持的对象列表（深拷贝）
    RegisteredAt time.Time    // 首次注册时间
    UpdatedAt    time.Time    // 最后更新时间
    ExpiresAt    time.Time    // 过期时间（UpdatedAt + Lifetime）
    RemoteAddr  net.Addr      // 设备地址
}

// IsExpired 检查注册是否已过期。
func (r *Registration) IsExpired() bool {
    return time.Now().After(r.ExpiresAt)
}

// Clone 返回深拷贝，防止调用方修改内部状态。
func (r *Registration) Clone() Registration {
    objects := make([]ObjectLink, len(r.Objects))
    for i, obj := range r.Objects {
        attrs := make(map[string]string, len(obj.Attributes))
        for k, v := range obj.Attributes {
            attrs[k] = v
        }
        objects[i] = ObjectLink{Path: obj.Path, Attributes: attrs}
    }
    return Registration{
        Endpoint:     r.Endpoint,
        LocationPath: r.LocationPath,
        Lifetime:     r.Lifetime,
        BindingMode:  r.BindingMode,
        Objects:      objects,
        RegisteredAt: r.RegisteredAt,
        UpdatedAt:    r.UpdatedAt,
        ExpiresAt:    r.ExpiresAt,
        RemoteAddr:   r.RemoteAddr,
    }
}
```

### 2.5 Content-Format 常量

```go
// internal/protocol/lwm2m/constants.go

// ContentFormat 是 CoAP Content-Format Option 的值。
type ContentFormat uint16

const (
    ContentFormatText        ContentFormat = 0     // text/plain
    ContentFormatLinkFormat  ContentFormat = 40    // application/link-format
    ContentFormatOpaque      ContentFormat = 42    // application/octet-stream
    ContentFormatJSON        ContentFormat = 50    // application/json
    ContentFormatLwM2MTLV    ContentFormat = 11542 // application/vnd.oma.lwm2m+tlv
    ContentFormatLwM2MJSON   ContentFormat = 11543 // application/vnd.oma.lwm2m+json
    ContentFormatSenMLJSON   ContentFormat = 110   // application/senml+json
    ContentFormatSenMLCBOR   ContentFormat = 112   // application/senml+cbor
)

// 常用 LwM2M 对象 ID
const (
    ObjectIDSecurity          = 0   // LwM2M Security
    ObjectIDServer            = 1   // LwM2M Server
    ObjectIDAccessControl     = 2   // Access Control
    ObjectIDDevice            = 3   // Device
    ObjectIDConnectivityMonitoring = 4
    ObjectIDFirmwareUpdate    = 5
    ObjectIDLocation          = 6
    ObjectIDConnectivityStatistics = 7
)

// 设备对象（Object 3）常用资源 ID
const (
    ResourceIDManufacturer    = 0
    ResourceIDModelNumber     = 1
    ResourceIDSerialNumber    = 2
    ResourceIDFirmwareVersion = 3
    ResourceIDReboot          = 4  // Execute
    ResourceIDFactoryReset    = 5  // Execute
    ResourceIDBatteryLevel    = 9
    ResourceIDMemoryFree      = 10
    ResourceIDCurrentTime     = 13
    ResourceIDUTCOffset       = 14
    ResourceIDTimezone        = 15
)
```

---

## 3. LwM2M Server

### 3.1 结构定义

```go
// internal/protocol/lwm2m/server.go

type Server struct {
    options     ServerOptions
    registry    map[string]*Registration // endpoint → Registration（内部状态）
    registryMu  sync.RWMutex

    // 注册事件回调
    onRegister   func(Registration)
    onUpdate     func(Registration)
    onDeregister func(endpoint string)

    // 生命周期
    stopCh   chan struct{}
    wg       sync.WaitGroup
    closed   atomic.Bool
}
```

### 3.2 注册操作

```go
// Register 处理客户端注册请求。
// 内部保存深拷贝，防止调用方修改内部注册表。
func (s *Server) Register(
    endpoint string,
    lifetime int,
    bindingMode string,
    objects []ObjectLink,
    remoteAddr net.Addr,
) (locationPath string, err error) {
    if endpoint == "" {
        return "", fmt.Errorf("%w: endpoint must not be empty",
            core.ErrInvalidMessage)
    }
    if lifetime <= 0 {
        return "", fmt.Errorf("%w: lifetime must be > 0",
            core.ErrInvalidMessage)
    }

    // 深拷贝 objects，防止调用方后续修改
    objectsCopy := make([]ObjectLink, len(objects))
    for i, obj := range objects {
        attrs := make(map[string]string, len(obj.Attributes))
        for k, v := range obj.Attributes {
            attrs[k] = v
        }
        objectsCopy[i] = ObjectLink{Path: obj.Path, Attributes: attrs}
    }

    now := time.Now()
    locationID := generateLocationID() // 随机短 ID（如 "abc123"）
    locationPath = "/rd/" + locationID

    registration := &Registration{
        Endpoint:     endpoint,
        LocationPath: locationPath,
        Lifetime:     lifetime,
        BindingMode:  bindingMode,
        Objects:      objectsCopy,
        RegisteredAt: now,
        UpdatedAt:    now,
        ExpiresAt:    now.Add(time.Duration(lifetime) * time.Second),
        RemoteAddr:   remoteAddr,
    }

    s.registryMu.Lock()
    s.registry[endpoint] = registration
    s.registryMu.Unlock()

    // 触发注册回调（传递深拷贝）
    if s.onRegister != nil {
        go s.onRegister(registration.Clone())
    }

    return locationPath, nil
}

// Update 更新已有注册记录的生命周期。
func (s *Server) Update(endpoint string, lifetime int) error {
    s.registryMu.Lock()
    defer s.registryMu.Unlock()

    reg, exists := s.registry[endpoint]
    if !exists {
        return fmt.Errorf("%w: endpoint %q not registered",
            core.ErrSessionNotFound, endpoint)
    }

    now := time.Now()
    reg.Lifetime = lifetime
    reg.UpdatedAt = now
    reg.ExpiresAt = now.Add(time.Duration(lifetime) * time.Second)

    if s.onUpdate != nil {
        go s.onUpdate(reg.Clone())
    }

    return nil
}

// Deregister 注销已有注册记录。
func (s *Server) Deregister(endpoint string) error {
    s.registryMu.Lock()
    _, exists := s.registry[endpoint]
    if !exists {
        s.registryMu.Unlock()
        return fmt.Errorf("%w: endpoint %q not registered",
            core.ErrSessionNotFound, endpoint)
    }
    delete(s.registry, endpoint)
    s.registryMu.Unlock()

    if s.onDeregister != nil {
        go s.onDeregister(endpoint)
    }

    return nil
}
```

### 3.3 注册表查询（防浅拷贝污染）

```go
// Registration 返回指定 endpoint 的注册记录（深拷贝）。
// 调用方修改返回值不影响内部状态。
func (s *Server) Registration(endpoint string) (Registration, bool) {
    s.registryMu.RLock()
    defer s.registryMu.RUnlock()

    reg, exists := s.registry[endpoint]
    if !exists {
        return Registration{}, false
    }
    return reg.Clone(), true // 返回深拷贝
}

// Registrations 返回所有注册记录的深拷贝列表。
func (s *Server) Registrations() []Registration {
    s.registryMu.RLock()
    defer s.registryMu.RUnlock()

    result := make([]Registration, 0, len(s.registry))
    for _, reg := range s.registry {
        result = append(result, reg.Clone()) // 每条记录深拷贝
    }
    return result
}
```

### 3.4 资源操作

```go
// ReadResource 向指定设备发送 Read 命令（CoAP GET）。
func (s *Server) ReadResource(
    ctx context.Context,
    endpoint string,
    path ObjectPath,
) ([]byte, error) {
    reg, exists := s.Registration(endpoint)
    if !exists {
        return nil, fmt.Errorf("%w: endpoint %q", core.ErrSessionNotFound, endpoint)
    }

    // 构造 CoAP GET 请求
    request := &coap.Message{
        Version:   1,
        Type:      coap.MsgTypeCON,
        Code:      coap.CodeGET,
        MessageID: s.nextMessageID(),
        Token:     generateToken(),
    }

    // 添加 Uri-Path Options
    for _, segment := range strings.Split(strings.TrimPrefix(path.String(), "/"), "/") {
        request.Options = append(request.Options, coap.Option{
            Number: coap.OptionUriPath,
            Value:  []byte(segment),
        })
    }

    return s.sendAndReceive(ctx, reg.RemoteAddr, request)
}

// WriteResource 向指定设备发送 Write 命令（CoAP PUT）。
func (s *Server) WriteResource(
    ctx context.Context,
    endpoint string,
    path ObjectPath,
    value []byte,
    contentFormat ContentFormat,
) error {
    reg, exists := s.Registration(endpoint)
    if !exists {
        return fmt.Errorf("%w: endpoint %q", core.ErrSessionNotFound, endpoint)
    }

    // 深拷贝 value，防止调用方复用切片
    valueCopy := make([]byte, len(value))
    copy(valueCopy, value)

    // 构造 CoAP PUT 请求
    request := &coap.Message{
        Version:   1,
        Type:      coap.MsgTypeCON,
        Code:      coap.CodePUT,
        MessageID: s.nextMessageID(),
        Token:     generateToken(),
        Payload:   valueCopy,
    }

    // 添加 Uri-Path Options
    for _, segment := range strings.Split(strings.TrimPrefix(path.String(), "/"), "/") {
        request.Options = append(request.Options, coap.Option{
            Number: coap.OptionUriPath,
            Value:  []byte(segment),
        })
    }

    // 添加 Content-Format Option
    cfBytes := make([]byte, 2)
    binary.BigEndian.PutUint16(cfBytes, uint16(contentFormat))
    request.Options = append(request.Options, coap.Option{
        Number: coap.OptionContentFormat,
        Value:  cfBytes,
    })

    _, err := s.sendAndReceive(ctx, reg.RemoteAddr, request)
    return err
}

// ExecuteResource 向指定设备发送 Execute 命令（CoAP POST）。
func (s *Server) ExecuteResource(
    ctx context.Context,
    endpoint string,
    path ObjectPath,
    args []byte,
) error {
    reg, exists := s.Registration(endpoint)
    if !exists {
        return fmt.Errorf("%w: endpoint %q", core.ErrSessionNotFound, endpoint)
    }

    request := &coap.Message{
        Version:   1,
        Type:      coap.MsgTypeCON,
        Code:      coap.CodePOST,
        MessageID: s.nextMessageID(),
        Token:     generateToken(),
        Payload:   args,
    }

    for _, segment := range strings.Split(strings.TrimPrefix(path.String(), "/"), "/") {
        request.Options = append(request.Options, coap.Option{
            Number: coap.OptionUriPath,
            Value:  []byte(segment),
        })
    }

    _, err := s.sendAndReceive(ctx, reg.RemoteAddr, request)
    return err
}
```

### 3.5 生命周期 sweep

```go
// Start 启动 LwM2M Server（启动过期注册清理）。
func (s *Server) Start() error {
    s.stopCh = make(chan struct{})
    s.wg.Add(1)
    go s.lifetimeSweep()
    return nil
}

// lifetimeSweep 定期清理过期注册记录。
func (s *Server) lifetimeSweep() {
    defer s.wg.Done()

    ticker := time.NewTicker(s.options.LifetimeCheckInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            s.sweepExpiredRegistrations()
        case <-s.stopCh:
            return
        }
    }
}

func (s *Server) sweepExpiredRegistrations() {
    s.registryMu.Lock()
    var expired []string
    for endpoint, reg := range s.registry {
        if reg.IsExpired() {
            expired = append(expired, endpoint)
        }
    }
    for _, endpoint := range expired {
        delete(s.registry, endpoint)
    }
    s.registryMu.Unlock()

    // 触发注销回调（锁外执行）
    for _, endpoint := range expired {
        if s.onDeregister != nil {
            go s.onDeregister(endpoint)
        }
    }
}

// Stop 停止 LwM2M Server。
func (s *Server) Stop() error {
    if s.closed.CompareAndSwap(false, true) {
        close(s.stopCh)
        s.wg.Wait()
    }
    return nil
}
```

---

## 4. LwM2M Client

### 4.1 结构定义

```go
// internal/protocol/lwm2m/client.go

type Client struct {
    options  ClientOptions
    conn     *net.UDPConn

    // 注册状态
    locationPath string
    registered   atomic.Bool

    // 资源表（objectPath → Resource）
    resources   map[string]Resource // key: path.String()
    resourcesMu sync.RWMutex

    // 生命周期
    stopCh    chan struct{}
    wg        sync.WaitGroup
    closed    atomic.Bool

    // MessageID 生成
    messageIDGen atomic.Uint32
}
```

### 4.2 Register 实现

```go
// Register 向 LwM2M Server 发送注册请求。
func (c *Client) Register(ctx context.Context) error {
    if c.registered.Load() {
        return errors.New("shark: lwm2m client already registered")
    }

    // 建立 UDP 连接（若尚未建立）
    if c.conn == nil {
        addr, err := net.ResolveUDPAddr("udp", c.options.ServerAddr)
        if err != nil {
            return fmt.Errorf("resolve server addr: %w", err)
        }
        conn, err := net.DialUDP("udp", nil, addr)
        if err != nil {
            return fmt.Errorf("dial udp: %w", err)
        }
        c.conn = conn
    }

    // 构造注册对象链接列表
    links := c.buildObjectLinks()

    // 构造 CoAP POST 请求（注册到 /rd）
    request := &coap.Message{
        Version:   1,
        Type:      coap.MsgTypeCON,
        Code:      coap.CodePOST,
        MessageID: uint16(c.messageIDGen.Add(1)),
        Token:     generateToken(),
        Payload:   []byte(FormatLinkFormat(links)),
    }

    // Uri-Path: /rd
    request.Options = append(request.Options,
        coap.Option{Number: coap.OptionUriPath, Value: []byte("rd")})

    // Uri-Query: ep={endpoint}&lt={lifetime}&b={binding}
    request.Options = append(request.Options,
        coap.Option{
            Number: coap.OptionUriQuery,
            Value:  []byte(fmt.Sprintf("ep=%s", c.options.Endpoint)),
        },
        coap.Option{
            Number: coap.OptionUriQuery,
            Value:  []byte(fmt.Sprintf("lt=%d", c.options.Lifetime)),
        },
        coap.Option{
            Number: coap.OptionUriQuery,
            Value:  []byte(fmt.Sprintf("b=%s", c.options.BindingMode)),
        },
    )

    // Content-Format: application/link-format
    cfBytes := make([]byte, 2)
    binary.BigEndian.PutUint16(cfBytes, uint16(ContentFormatLinkFormat))
    request.Options = append(request.Options,
        coap.Option{Number: coap.OptionContentFormat, Value: cfBytes})

    // 发送并等待响应
    response, err := c.sendRequest(ctx, request)
    if err != nil {
        return fmt.Errorf("register request: %w", err)
    }

    // 检查响应码（期望 2.01 Created）
    if response.Code != coap.CodeCreated {
        return fmt.Errorf("register failed: response code %v", response.Code)
    }

    // 提取 Location-Path（注册路径）
    c.locationPath = extractLocationPath(response)
    c.registered.Store(true)

    // 启动自动更新 goroutine
    c.wg.Add(1)
    go c.updateLoop()

    return nil
}

// extractLocationPath 从响应的 Location-Path Options 中提取路径。
func extractLocationPath(response *coap.Message) string {
    var parts []string
    for _, opt := range response.Options {
        if opt.Number == coap.OptionLocationPath {
            parts = append(parts, string(opt.Value))
        }
    }
    if len(parts) == 0 {
        return ""
    }
    return "/" + strings.Join(parts, "/")
}
```

### 4.3 Update 与 Deregister

```go
// Update 发送注册更新请求，延长 lifetime。
func (c *Client) Update(ctx context.Context) error {
    if !c.registered.Load() {
        return errors.New("shark: lwm2m client not registered")
    }

    // 解析 locationPath 为 Uri-Path segments
    segments := strings.Split(strings.TrimPrefix(c.locationPath, "/"), "/")

    request := &coap.Message{
        Version:   1,
        Type:      coap.MsgTypeCON,
        Code:      coap.CodePOST,
        MessageID: uint16(c.messageIDGen.Add(1)),
        Token:     generateToken(),
    }

    for _, seg := range segments {
        request.Options = append(request.Options,
            coap.Option{Number: coap.OptionUriPath, Value: []byte(seg)})
    }

    // Uri-Query: lt={lifetime}
    request.Options = append(request.Options,
        coap.Option{
            Number: coap.OptionUriQuery,
            Value:  []byte(fmt.Sprintf("lt=%d", c.options.Lifetime)),
        })

    response, err := c.sendRequest(ctx, request)
    if err != nil {
        return fmt.Errorf("update request: %w", err)
    }

    // 期望 2.04 Changed
    if response.Code != coap.CodeChanged {
        return fmt.Errorf("update failed: response code %v", response.Code)
    }

    return nil
}

// Deregister 发送注销请求。
func (c *Client) Deregister(ctx context.Context) error {
    if !c.registered.Load() {
        return nil // 未注册，幂等
    }

    segments := strings.Split(strings.TrimPrefix(c.locationPath, "/"), "/")

    request := &coap.Message{
        Version:   1,
        Type:      coap.MsgTypeCON,
        Code:      coap.CodeDELETE,
        MessageID: uint16(c.messageIDGen.Add(1)),
        Token:     generateToken(),
    }

    for _, seg := range segments {
        request.Options = append(request.Options,
            coap.Option{Number: coap.OptionUriPath, Value: []byte(seg)})
    }

    response, err := c.sendRequest(ctx, request)
    if err != nil {
        return fmt.Errorf("deregister request: %w", err)
    }

    // 期望 2.02 Deleted
    if response.Code != coap.CodeDeleted {
        return fmt.Errorf("deregister failed: response code %v", response.Code)
    }

    c.registered.Store(false)
    c.locationPath = ""
    return nil
}
```

### 4.4 自动更新 Loop

```go
// updateLoop 在 lifetime/2 时自动发送 Update，保持注册有效。
func (c *Client) updateLoop() {
    defer c.wg.Done()

    interval := time.Duration(c.options.Lifetime/2) * time.Second
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            ctx, cancel := context.WithTimeout(
                context.Background(), c.options.AckTimeout)
            if err := c.Update(ctx); err != nil {
                // 更新失败记录日志，不立即注销
                // 下次 tick 再重试
            }
            cancel()
        case <-c.stopCh:
            return
        }
    }
}
```

### 4.5 资源管理

```go
// AddResource 注册资源到客户端本地资源表。
// 深拷贝 resource.Value，防止调用方切片复用。
func (c *Client) AddResource(path ObjectPath, resource Resource) {
    // 深拷贝 Value
    valueCopy := make([]byte, len(resource.Value))
    copy(valueCopy, resource.Value)
    resource.Value = valueCopy

    c.resourcesMu.Lock()
    c.resources[path.String()] = resource
    c.resourcesMu.Unlock()
}

// GetResource 获取资源当前值（调用 OnRead 回调）。
func (c *Client) GetResource(path ObjectPath) ([]byte, error) {
    c.resourcesMu.RLock()
    resource, exists := c.resources[path.String()]
    c.resourcesMu.RUnlock()

    if !exists {
        return nil, fmt.Errorf("%w: resource %s not found",
            core.ErrStoreNotFound, path)
    }

    if resource.OnRead != nil {
        return resource.OnRead()
    }
    return resource.Value, nil
}

// SetResource 设置资源值（调用 OnWrite 回调）。
func (c *Client) SetResource(path ObjectPath, value []byte) error {
    c.resourcesMu.RLock()
    resource, exists := c.resources[path.String()]
    c.resourcesMu.RUnlock()

    if !exists {
        return fmt.Errorf("%w: resource %s not found",
            core.ErrStoreNotFound, path)
    }

    if resource.OnWrite != nil {
        return resource.OnWrite(value)
    }

    // 更新本地值（深拷贝）
    valueCopy := make([]byte, len(value))
    copy(valueCopy, value)

    c.resourcesMu.Lock()
    resource.Value = valueCopy
    c.resources[path.String()] = resource
    c.resourcesMu.Unlock()

    return nil
}

// buildObjectLinks 构造注册时的对象链接列表。
func (c *Client) buildObjectLinks() []ObjectLink {
    c.resourcesMu.RLock()
    defer c.resourcesMu.RUnlock()

    // 按 ObjectID/InstanceID 聚合资源
    objectSet := make(map[string]ObjectPath)
    for pathStr := range c.resources {
        path, err := ParsePath(pathStr)
        if err != nil {
            continue
        }
        // 只包含到实例级别（/OID/IID）
        instancePath := ObjectPath{
            ObjectID:    path.ObjectID,
            InstanceID:  path.InstanceID,
            HasInstance: path.HasInstance,
        }
        objectSet[instancePath.String()] = instancePath
    }

    links := make([]ObjectLink, 0, len(objectSet))
    for _, path := range objectSet {
        links = append(links, ObjectLink{
            Path:       path,
            Attributes: make(map[string]string),
        })
    }

    // 排序保证输出稳定
    slices.SortFunc(links, func(a, b ObjectLink) int {
        if a.Path.ObjectID != b.Path.ObjectID {
            return int(a.Path.ObjectID) - int(b.Path.ObjectID)
        }
        return int(a.Path.InstanceID) - int(b.Path.InstanceID)
    })

    return links
}
```

### 4.6 sendRequest 辅助实现

```go
// sendRequest 发送 CoAP 请求并等待响应（简单请求/响应模型）。
func (c *Client) sendRequest(ctx context.Context, request *coap.Message) (*coap.Message, error) {
    // 序列化请求
    data, err := request.Marshal()
    if err != nil {
        return nil, fmt.Errorf("marshal request: %w", err)
    }

    // 设置写超时
    if deadline, ok := ctx.Deadline(); ok {
        c.conn.SetWriteDeadline(deadline)
    }

    if _, err := c.conn.Write(data); err != nil {
        return nil, fmt.Errorf("send request: %w", err)
    }

    // 等待响应（CON 需要 ACK）
    buf := make([]byte, 65535)
    if deadline, ok := ctx.Deadline(); ok {
        c.conn.SetReadDeadline(deadline)
    }

    n, err := c.conn.Read(buf)
    if err != nil {
        return nil, fmt.Errorf("receive response: %w", err)
    }

    response, err := coap.Parse(buf[:n])
    if err != nil {
        return nil, fmt.Errorf("parse response: %w", err)
    }

    // 验证 Token 一致性
    if !bytes.Equal(response.Token, request.Token) {
        return nil, fmt.Errorf("%w: response token mismatch",
            core.ErrCoAPInvalidMessage)
    }

    return response, nil
}
```

### 4.7 Close

```go
// Close 关闭 LwM2M Client（注销并释放资源）。
func (c *Client) Close(ctx context.Context) error {
    if c.closed.CompareAndSwap(false, true) {
        // 停止 updateLoop
        if c.stopCh != nil {
            close(c.stopCh)
        }
        c.wg.Wait()

        // 注销（若已注册）
        if c.registered.Load() {
            c.Deregister(ctx) // 忽略错误（关闭时尽力注销）
        }

        if c.conn != nil {
            return c.conn.Close()
        }
    }
    return nil
}
```

---

## 5. CoAP 接入层

### 5.1 responder.go 职责

```go
// internal/protocol/lwm2m/responder.go
//
// Responder 将 LwM2M 语义操作与 CoAP Server 绑定。
// 接收 CoAP transport 传递的已解析消息，
// 根据 Uri-Path 分发到 LwM2M Server 的操作方法。
```

### 5.2 Responder 结构

```go
type Responder struct {
    server *Server
}

func NewResponder(server *Server) *Responder {
    return &Responder{server: server}
}

// Handler 返回可注册到 CoAP Server 的 core.Handler。
func (r *Responder) Handler() core.Handler {
    return func(sess core.Session, msg core.Message) error {
        // 解析 CoAP 消息（已由 CoAP transport 处理帧层）
        coapMsg, err := coap.Parse(msg.Payload)
        if err != nil {
            return err
        }

        path := coapMsg.GetUriPath()
        query := coapMsg.GetUriQuery()

        return r.dispatch(sess, coapMsg, path, query)
    }
}
```

### 5.3 请求分发

```go
func (r *Responder) dispatch(
    sess core.Session,
    msg *coap.Message,
    path string,
    query map[string]string,
) error {
    switch {
    // POST /rd → Register
    case msg.Code == coap.CodePOST && path == "/rd":
        return r.handleRegister(sess, msg, query)

    // POST /rd/{id} → Update
    case msg.Code == coap.CodePOST && strings.HasPrefix(path, "/rd/"):
        endpoint := r.findEndpointByLocation(path)
        return r.handleUpdate(sess, msg, endpoint, query)

    // DELETE /rd/{id} → Deregister
    case msg.Code == coap.CodeDELETE && strings.HasPrefix(path, "/rd/"):
        endpoint := r.findEndpointByLocation(path)
        return r.handleDeregister(sess, msg, endpoint)

    default:
        // 未知路径或方法
        ack, _ := coap.NewACK(msg, coap.CodeBadRequest, nil).Marshal()
        return sess.Send(ack)
    }
}

func (r *Responder) handleRegister(
    sess core.Session,
    msg *coap.Message,
    query map[string]string,
) error {
    endpoint := query["ep"]
    lifetimeStr := query["lt"]
    bindingMode := query["b"]

    if endpoint == "" {
        ack, _ := coap.NewACK(msg, coap.CodeBadRequest,
            []byte("missing ep parameter")).Marshal()
        return sess.Send(ack)
    }

    lifetime := 86400 // 默认 24 小时
    if lifetimeStr != "" {
        if n, err := strconv.Atoi(lifetimeStr); err == nil && n > 0 {
            lifetime = n
        }
    }

    // 解析对象链接（Link-Format payload）
    links, err := ParseLinkFormat(string(msg.Payload))
    if err != nil {
        ack, _ := coap.NewACK(msg, coap.CodeBadRequest,
            []byte("invalid link-format payload")).Marshal()
        return sess.Send(ack)
    }

    locationPath, err := r.server.Register(
        endpoint, lifetime, bindingMode, links, sess.RemoteAddr())
    if err != nil {
        ack, _ := coap.NewACK(msg, coap.CodeInternalServerError,
            []byte(err.Error())).Marshal()
        return sess.Send(ack)
    }

    // 构造 2.01 Created 响应，附带 Location-Path
    response := &coap.Message{
        Version:   1,
        Type:      coap.MsgTypeACK,
        Code:      coap.CodeCreated,
        MessageID: msg.MessageID,
        Token:     msg.Token,
    }

    // 添加 Location-Path Options
    for _, segment := range strings.Split(
        strings.TrimPrefix(locationPath, "/"), "/") {
        response.Options = append(response.Options, coap.Option{
            Number: coap.OptionLocationPath,
            Value:  []byte(segment),
        })
    }

    ackData, err := response.Marshal()
    if err != nil {
        return err
    }
    return sess.Send(ackData)
}

func (r *Responder) handleUpdate(
    sess core.Session,
    msg *coap.Message,
    endpoint string,
    query map[string]string,
) error {
    if endpoint == "" {
        ack, _ := coap.NewACK(msg, coap.CodeNotFound, nil).Marshal()
        return sess.Send(ack)
    }

    lifetime := 86400
    if lt := query["lt"]; lt != "" {
        if n, err := strconv.Atoi(lt); err == nil && n > 0 {
            lifetime = n
        }
    }

    if err := r.server.Update(endpoint, lifetime); err != nil {
        if errors.Is(err, core.ErrSessionNotFound) {
            ack, _ := coap.NewACK(msg, coap.CodeNotFound, nil).Marshal()
            return sess.Send(ack)
        }
        ack, _ := coap.NewACK(msg, coap.CodeInternalServerError, nil).Marshal()
        return sess.Send(ack)
    }

    ack, _ := coap.NewACK(msg, coap.CodeChanged, nil).Marshal()
    return sess.Send(ack)
}

func (r *Responder) handleDeregister(
    sess core.Session,
    msg *coap.Message,
    endpoint string,
) error {
    if endpoint == "" {
        ack, _ := coap.NewACK(msg, coap.CodeNotFound, nil).Marshal()
        return sess.Send(ack)
    }

    if err := r.server.Deregister(endpoint); err != nil {
        if errors.Is(err, core.ErrSessionNotFound) {
            ack, _ := coap.NewACK(msg, coap.CodeNotFound, nil).Marshal()
            return sess.Send(ack)
        }
        ack, _ := coap.NewACK(msg, coap.CodeInternalServerError, nil).Marshal()
        return sess.Send(ack)
    }

    ack, _ := coap.NewACK(msg, coap.CodeDeleted, nil).Marshal()
    return sess.Send(ack)
}

// findEndpointByLocation 通过 LocationPath 查找 endpoint。
func (r *Responder) findEndpointByLocation(locationPath string) string {
    for _, reg := range r.server.Registrations() {
        if reg.LocationPath == locationPath {
            return reg.Endpoint
        }
    }
    return ""
}
```

---

## 6. 配置项完整参考

### 6.1 ServerOptions

```go
// internal/protocol/lwm2m/options.go
type ServerOptions struct {
    Addr                  string        // 默认 "0.0.0.0:5783"
    DefaultLifetime       int           // 默认 86400s（24 小时）
    LifetimeCheckInterval time.Duration // 默认 60s（过期检查间隔）
    MaxRegistrations      int           // 默认 10000（0=不限制）
}

func defaultServerOptions() ServerOptions {
    return ServerOptions{
        Addr:                  "0.0.0.0:5783",
        DefaultLifetime:       86400,
        LifetimeCheckInterval: 60 * time.Second,
        MaxRegistrations:      10000,
    }
}

type ServerOption func(*ServerOptions)

func WithServerAddr(addr string) ServerOption {
    return func(o *ServerOptions) { o.Addr = addr }
}

func WithDefaultLifetime(seconds int) ServerOption {
    return func(o *ServerOptions) { o.DefaultLifetime = seconds }
}

func WithLifetimeCheckInterval(d time.Duration) ServerOption {
    return func(o *ServerOptions) { o.LifetimeCheckInterval = d }
}

func WithMaxRegistrations(max int) ServerOption {
    return func(o *ServerOptions) { o.MaxRegistrations = max }
}

// 注册事件回调 Options
func WithOnRegister(fn func(Registration)) ServerOption {
    return func(o *ServerOptions) { /* 存储到 Server，非 Options */ }
}
```

### 6.2 ClientOptions

```go
type ClientOptions struct {
    ServerAddr  string        // 默认 "127.0.0.1:5783"
    LocalAddr   string        // 默认 ""（系统自动分配）
    Endpoint    string        // 必填：设备端点名称
    Lifetime    int           // 默认 86400s
    BindingMode string        // 默认 "U"（UDP）
    AckTimeout  time.Duration // 默认 10s（等待 ACK 超时）
}

func defaultClientOptions() ClientOptions {
    return ClientOptions{
        ServerAddr:  "127.0.0.1:5783",
        Endpoint:    "unknown-device",
        Lifetime:    86400,
        BindingMode: "U",
        AckTimeout:  10 * time.Second,
    }
}

type ClientOption func(*ClientOptions)

func WithServerAddr(addr string) ClientOption {
    return func(o *ClientOptions) { o.ServerAddr = addr }
}

func WithEndpoint(endpoint string) ClientOption {
    return func(o *ClientOptions) { o.Endpoint = endpoint }
}

func WithLifetime(seconds int) ClientOption {
    return func(o *ClientOptions) { o.Lifetime = seconds }
}

func WithBindingMode(mode string) ClientOption {
    return func(o *ClientOptions) { o.BindingMode = mode }
}

func WithAckTimeout(d time.Duration) ClientOption {
    return func(o *ClientOptions) { o.AckTimeout = d }
}

// NewServer 构造函数
func NewServer(opts ...ServerOption) *Server {
    options := defaultServerOptions()
    for _, opt := range opts {
        opt(&options)
    }
    return &Server{
        options:  options,
        registry: make(map[string]*Registration),
        stopCh:   make(chan struct{}),
    }
}

// NewClient 构造函数
func NewClient(opts ...ClientOption) *Client {
    options := defaultClientOptions()
    for _, opt := range opts {
        opt(&options)
    }
    return &Client{
        options:   options,
        resources: make(map[string]Resource),
        stopCh:    make(chan struct{}),
    }
}
```

### 6.3 配置使用示例

```go
// LwM2M Server 示例
lwm2mServer := lwm2m.NewServer(
    lwm2m.WithServerAddr(":5783"),
    lwm2m.WithDefaultLifetime(3600),
    lwm2m.WithLifetimeCheckInterval(30*time.Second),
)
lwm2mServer.SetOnRegister(func(reg lwm2m.Registration) {
    log.Printf("device registered: %s, objects: %v",
        reg.Endpoint, reg.Objects)
})

// 将 LwM2M Responder 绑定到 CoAP Server
responder := lwm2m.NewResponder(lwm2mServer)
coapServer := coap.NewServer(responder.Handler(),
    coap.WithAddr(":5783"),
)

// LwM2M Client 示例
client := lwm2m.NewClient(
    lwm2m.WithServerAddr("192.168.1.100:5783"),
    lwm2m.WithEndpoint("sensor-001"),
    lwm2m.WithLifetime(3600),
)

// 注册资源
client.AddResource(
    lwm2m.ObjectPath{ObjectID: 3, InstanceID: 0,
        ResourceID: 0, HasInstance: true, HasResource: true},
    lwm2m.NewResource(0, []byte("ACME Corp")),
)

// 注册到 Server
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
if err := client.Register(ctx); err != nil {
    log.Fatal("register failed:", err)
}
```
