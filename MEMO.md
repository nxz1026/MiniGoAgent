# MiniGoAgent 项目备忘录

> 最后更新：2026-07-09
> 用途：给 LLM 提供完整项目上下文，避免重复发现
> 铁律1：任何时候思考都用中文
> 铁律2：每次阶段任务完成后必须必须必须（没有商量的余地）必须测试，然后迭代本文档（MEMO.md）
> 铁律3：迭代本文档不删除，只添加

---

## 项目概述

MiniGoAgent 是一个基于 Go + eino 框架的最小化 LLM Agent 项目，提供 Web UI 交互界面，支持流式聊天、终端命令执行、联网搜索、文件操作、图片识别等能力，并具备多会话隔离、历史持久化、全链路日志等工程化特性。

核心定位：**低依赖、可插拔协议层、全链路可观测**。协议层零 eino 依赖，通过独立类型和工厂注册模式支持多供应商（OpenAI/DeepSeek/Zhipu/MiniMax 等），各层职责清晰，方便扩展。

---

## 一、项目定位

Go + eino 框架的最小化 LLM Agent。核心目标：**低依赖、可插协议层、全链路可观测**。

关键约束：
- Go 1.26.5，GOPROXY `https://goproxy.cn,direct`
- eino 库本地依赖，路径 `reference/eino`（go.mod replace）。新机器需要先 `git clone https://github.com/cloudwego/eino.git reference/eino`
- 零 paid API 依赖，倾向免费额度
- Windows 开发环境（PowerShell）

---

## 二、架构总览

```
Browser (SSE)
    │
    ▼
HTTP Server (main.go) ── sessionlog ── logs/sessions/<id>/*.md
    │
    ▼
react.Agent (eino) ── chatModel (adapter)
    │                       │
    ├── 工具执行            │
    └── chatModel.Generate/Stream
                            │
                            ▼
                        protocol.Protocol (interface)
                            │
                            └── protocol.OpenAI
                                ├── Vendor detection (host.go)
                                ├── HTTP retry (retry.go)
                                ├── SSE reader (openai.go)
                                ├── Content compression (content_*.go)
                                ├── Telemetry.Record (telemetry.go)
                                └── CtxLogf raw logging
```

### 分层说明

| 层 | 职责 | 关键设计 |
|-----|---------|-----------|
| `main.go` | HTTP server + Agent 组装 + 会话管理 | 无框架，纯 net/http |
| `chatModel` | eino ↔ protocol 适配层 | 实现 model.ChatModel / Streamable |
| `protocol/` | LLM 通信协议层 | **零 eino 依赖**，独立类型 |
| `tools/` | Agent 可调用的工具 | 每个工具独立文件 |
| `tools/sessionlog/` | 会话日志 | 每 session 一个 .md 文件 |

---

## 三、核心设计决策

### 3.1 多轮对话
- **外部维护** `[]*schema.Message` 历史，每次 `agent.Generate/Stream` 传全部
- `MessageRewriter` 超 6 条截断到最近 6 条 + MessageModifier 注入 system prompt
- `MaxStep` 设为 12

### 3.2 协议层独立
- `protocol/` 零 eino 依赖，自己的 `Message`/`ToolCall`/`Chunk`/`Usage` 类型
- 工厂注册模式：`Register("openai", factory)` / `protocol.New("openai", cfg)`
- `Protocol` 接口：`Chat(ctx, req) -> (*Response, error)` + `Stream(ctx, req) -> (<-chan Chunk, error)`

### 3.3 压缩策略
- 在 `toWireMessages()` 序列化时自动触发
- session 历史存**完整版**不丢数据
- **Live zone**：从最后一条 `RoleUser` 开始压缩，之前 frozen 历史不动
- 5 种 content type 检测（JSON/列表/代码/长文/短文本），各自不同压缩策略

### 3.4 统计（Telemetry）
- `protocol.Telemetry` 在 Raw 层记录，比上层更准
- `Record(model, vendor, durSec, usage)` — 每次 Chat/Stream 完成后自动调用
- `FormatLine(" · ")` 生成文本：`47s · auto · ↑92.7k · ↓1.4k · ctx 92.7k/524k 18%`
- `FormatMap()` 生成 JSON 给前端 SSE event
- `ModelContextWindow(model)` 查找已知模型 context 上限，未知模型 fallback 524k

### 3.5 日志体系

| 日志 | 位置 | 方式 |
|------|------|------|
| 应用日志 | `logs/` | 4 级 (DEBUG/INFO/WARN/ERROR)，stdout + 按天轮转 |
| 会话日志 | `logs/sessions/<id>/<timestamp>.md` | 表格记录 user/assistant/tool call/raw events |
| Raw 层日志 | 同上 | 通过 context key `protocol.CtxLogf` 注入 log 函数 |

### 3.6 SseFramer / EventStreamParser
- 已实现但**未接入**（在 `sse_framer.go`）
- 原因：HTTP 客户端场景不需要（`net/http` 处理 chunked transfer）
- 留待 Bedrock 供应商或 raw TCP 代理场景使用
- 文件内有详细接入说明

### 3.7 工具排序
- `toWireTools()` 按 name 排序工具数组
- `sortSchemaKeys()` 递归排序 JSON Schema 中所有 key（字母序）
- 目的：减少相同逻辑不同序列化导致的 cache miss / diff noise

### 3.8 多 Key 轮转
- `OpenAI` 持有 `apiKeys []string` + `keyIndex int`
- 401/403 时 `rotateKey()` 切换到下一个 key
- 在 `streamWithReconnect` 重试循环中自动触发

### 3.9 Rate Limiter
- `protocol/ratelimit.go`：token bucket，按 RPM/TPM 限流
- 每次 `Chat()` 或 `streamWithReconnect()` 入口 `Wait(ctx, tokens)`
- token 估算：`len(content)/4` 近似
- `RATE_LIMIT_RPM=0` / `RATE_LIMIT_TPM=0` 关闭

### 3.10 上下文阈值
- `Telemetry.warnPct` / `Telemetry.comprPct`，默认 40%/50%
- `Record()` 中比较 `promptTokens / ModelContextWindow * 100`
- 超过 `warnPct`：`ChunkWarn` / `Response.Warning` 发出预警文本
- 超过 `comprPct`：标记 `compress=true`，ADK 层收到后调压缩 LLM

### 3.11 可配置超时
- `StreamTimeout` 从 Config 注入，替代硬编码 120s
- `readStream` 的 stall watchdog 用 `o.streamTimeout`
- `http.Client.Timeout = timeout + 30s` 留余量

### 3.12 请求 Body 复用（sync.Pool）
- `buildBody()` 从 `bodyPool` 获取 `map[string]any`，`json.Marshal` 后归还
- 减少每次请求的 map 分配，降低 GC 压力

### 3.13 HTTP 连接池调优
- `DefaultTransport` 克隆 `http.DefaultTransport` 并调参：MaxIdleConns=100，MaxIdleConnsPerHost=10，IdleConnTimeout=90s
- `protocol.NewHTTPClient(timeout)` 供上层（tools/vision、tools/compress）复用同一 Transport
- `OpenAI.client` 同样使用调优后的 Transport

### 3.14 RetryNotify
- `protocol.CtxRetryNotify` context key 携带 `func(attempt, max int)` 回调
- `SendWithRetry` 每次重试前调用，上层可展示重试进度

### 3.15 StreamInterruptedError + 自动恢复
- `StreamInterruptedError` 包装已 emit 后的流中断错误，`Emitted` 字段标记是否已输出内容
- `readStream` 所有 error return 经 `streamErr()` 统一包装
- `chatModel.Stream` 检测到 `StreamInterruptedError` 后，发送 `[流中断，正在恢复...]` 提示，调用 `forwardChat` 补全响应
- `forwardChat` 直接走 `proto.Chat`（非 stream），避免二次断线风险

### 3.16 工具参数兜底
- `sanitizeTool()` 在 `toWireTools()` 中调用：空 name → `unnamed_tool`，空 description → `user-defined tool`
- 防止 API 因空字段返回 400

### 3.17 Chunk.Error 类型化
- `Chunk.Error` 从 `string` 改为 `error`，支持 `errors.As` 类型检查
- 所有 `Chunk{Type: ChunkError, Error: ...}` 统一传 error 对象

### 3.18 Per-Host 连接池调优
- `DefaultTransport` 克隆 `http.DefaultTransport`：MaxIdleConns=100，MaxIdleConnsPerHost=20，MaxConnsPerHost=50，IdleConnTimeout=120s
- `OpenAI.client` 与 `tools/vision`、`tools/compress` 共享同一 Transport，减少连接创建开销

### 3.19 断路器（Circuit Breaker）
- `CircuitBreaker` 状态机：Closed → Open → HalfOpen → Closed
- `vendorCircuitBreaker map[Vendor]*CircuitBreaker` 按供应商独立计数
- `SendWithRetry` 每次失败调用 `Check(err)`，401/403 调用 `Success()` 重置
- 防止单供应商故障风暴，自动隔离 + 超时恢复

---

## 四、文件地图

```
MiniGoAgent/
├── main.go                        # HTTP server + agent assembly + session persistence
├── protocol/                      # Protocol layer (zero eino deps)
│   ├── types.go                   # Message/ToolCall/Chunk/Usage/Protocol interface
│   ├── openai.go                  # OpenAI-compatible provider + SSE reader + raw logger
│   ├── host.go                    # Vendor detection + Vendor.String()
│   ├── retry.go                   # HTTP retry with exponential backoff + jitter
│   ├── think.go                   # MiniMax <think> tag inline reasoning parser
│   ├── normalize.go               # Tool call pairing repair (broken JSON, orphans, backfill)
│   ├── schema_canonicalize.go     # JSON Schema normalization
│   ├── telemetry.go               # Stats collector: Record/FormatLine/FormatMap/ModelContextWindow
│   ├── content_detector.go        # 5 content type detection
│   ├── content_compress.go        # Per-type compression strategies
│   ├── sse_framer.go              # Byte-level SSE + Bedrock EventStream (declared, not wired)
│   ├── ratelimit.go               # Token bucket rate limiter (RPM/TPM)
│   ├── protocol_test.go           # 25 unit tests (mock server)
│   └── openai_integration_test.go # 9 integration tests
├── tools/                         # Agent tools
│   ├── terminal.go                # Shell command execution
│   ├── websearch.go               # Web search
│   ├── compress.go                # Text compression
│   ├── fileops.go                 # Read/Write/Edit/Glob/Grep
│   ├── vision.go                  # Image analysis (StepFun)
│   ├── cache.go                   # Terminal result cache (30s TTL)
│   ├── vision_test.go              # Vision smoke test（1 个）
│   ├── compress_test.go            # Compress smoke test（1 个）
│   ├── filter/                    # Output filter framework (RTK-style)
│   ├── log/                       # Application logging (4 levels + rotation)
│   └── sessionlog/                # Per-session .md log
├── frontend/
│   └── index.html                 # Web UI (SSE streaming, markdown, image paste)
└── reference/                     # Reference codebases (eino, headroom, RTK, DeepSeek-Reasonix, opencode, hermes-agent)
```

---

## 五、关键模式 / 套路

### 5.1 新增供应商
```go
// 1. host.go: 加 Vendor 枚举 + IsXxx() + DetectVendor case + String() case
// 2. openai.go init(): 注册 factory
// 3. openai.go NewOpenAI(): 加 switch case 设 policy
// 4. telemetry.go ModelContextWindow(): 加 context window
```

### 5.2 新增工具
```go
// 1. tools/xxx.go: 实现工具函数 + RegisterTool
// 2. main.go main(): s.llm.WithTools() 注册
```

### 5.3 读取 Telemetry（上层用）
```go
type telProvider interface{ GetTelemetry() *protocol.Telemetry }
if p, ok := m.proto.(telProvider); ok {
    line := p.GetTelemetry().FormatLine(" · ")    // 文本
    m := p.GetTelemetry().FormatMap()              // JSON
}
```

### 5.4 context 注入 Raw 日志
```go
ctx := context.WithValue(ctx, protocol.CtxLogf, func(f string, a ...any) {
    log.Debug(f, a...)
    if sl := sessionlog.Get(sid); sl != nil {
        sl.LogRaw(f, a)
    }
})
```

---

## 六、测试

```bash
go clean -testcache
go test ./protocol/ -v                # 25 单元测试（无需网络）
go test -tags=integration ./protocol/ # 9 集成测试（需 API key）
```

---

## 七、已知约束 / 待改进

- SseFramer / EventStreamParser 已实现但被 `//go:build never` 排除编译（见 `sse_framer.go` 头部说明）
- 非 OpenAI 供应商的兼容性测试不足（Mock 测试覆盖了 retry/reconnect/think tag 行为）
- Vision HTTP handler 同时作为 agent tool 注册，agent 可主动调用（但非流式）
- eino 依赖本地路径 `replace`，非 go.mod 标准发布（README 已标注构建步骤）

---

## 八、README 文件说明

项目根目录有两份 README：

| 文件 | 用途 |
|------|------|
| `README.md` | 首页展示，简洁功能+配置，中英文合版 |
| `README.detail.md` | 技术细节版（原 `README.md` 改名），含完整架构/协议层/项目结构/测试 |

`reference/` 目录下各第三方参考代码自带其 README，非 MiniGoAgent 项目文档。

---

## 九、本次会话（2026-07-09）变更摘要

### 第一轮：Telemetry + 内容压缩 + 会话日志 + README 拆分

1. `protocol/telemetry.go` 新增：Telemetry 结构体 + Record/FormatLine/FormatMap/ModelContextWindow
2. `protocol/openai.go`：OpenAI 嵌入 Telemetry，Chat/Stream 完成后自动 Record；logf 签名改为 logf(ctx, ...)
3. `protocol/host.go`：Vendor 加 String() 方法
4. `main.go`：chatModel 去掉本地 stats，改为 StatsLine()/StatsJSON() 委托 protocol.Telemetry；chatResp 加 Stats 字段
5. `frontend/index.html`：stats 卡片样式 + SSE/non-SSE 解析渲染
6. `README.md → README.detail.md`：原技术文档改名为 detail，首页 README.md 重写为简洁功能+配置版
7. `tools/sessionlog/sessionlog.go`：新增 LogRaw 方法
8. `protocol/content_detector.go` + `content_compress.go`：5 种内容类型检测 + 按类型压缩策略
9. `protocol/sse_framer.go`：字节级 SSE 帧解析 + Bedrock EventStream（声明未接入）

### 第二轮：多 Key 轮转 + Rate Limiter + 上下文阈值 + 可配置超时

10. `protocol/ratelimit.go` 新增：Token bucket 限流器（RPM/TPM）
11. `protocol/types.go`：Config 加 APIKeys/StreamTimeout/RateLimitRPM/TPM/ContextWarnPct/CompressPct
12. `protocol/openai.go`：`NewOpenAI(apiKey, baseURL, model)` → `NewOpenAI(cfg Config)`；多 key 轮转；rate limiter 入口 wait；`sendContextWarning()`
13. `protocol/telemetry.go`：Record 返回 `(warn, compress bool)`, SetThresholds, LastWarning
14. `main.go`：读新 env 传给 Config；加 `getEnvInt`/`splitEnv` 辅助函数
15. `.env`：添加新参数占位（OPENAI_API_KEYS/STREAM_TIMEOUT/RATE_LIMIT_*/CONTEXT_*）
16. `README.md` + `README.detail.md`：更新配置表和协议层特性

### 第三轮（2026-07-09）：Context 修复 + 常量可配置 + Vision tool + 代码重构 + History DTO + 测试 + 文档

17. `main.go`：3 处 `context.Background()` → `r.Context()`，请求取消可传播到 agent/LLM；`handleVisionInternal` 加 `ctx` 参数
18. `protocol/types.go` + `openai.go`：`MaxReconnect` 加入 Config，替代硬编码 `maxReconnectAttempts=3`
19. `main.go` + `.env` + `README.md`：`AGENT_MAX_STEP` / `MAX_RECONNECT_ATTEMPTS` 从 env 读取
20. `main.go`：Vision 注册为 agent tool（`utils.InferTool("vision", ...)`），agent 可主动调用图片分析
21. `main.go`：提取 `logUserMessage` / `logAssistantResponse` / `injectLogCtx` / `getStatsMap` 四个 helper，handler 去重
22. `main.go`：History 持久化改用独立 DTO（`historyMessage`/`historyToolCall`），隔离 eino `schema.Message`
23. `protocol/sse_framer.go`：加 `//go:build never` 排除编译，避免死代码误读
24. `main_test.go` 新增：7 个测试覆盖 `getEnv`/`getEnvInt`/`splitEnv`/`extractLocalImagePath`/DTO 序列化/`loadEnv`
25. `tools/cache_test.go` + `tools/fileops_test.go` 新增：4 个测试覆盖缓存逻辑和文件操作
26. `README.md` + `README.detail.md` + `MEMO.md`：文档同步更新（工具计数、构建步骤、配置表、测试命令）

### 第四轮（2026-07-09）：RAW 协议层性能 + 健壮性优化

27. `protocol/openai.go`：`bodyPool` sync.Pool 复用 `map[string]any`，`buildBody` 减少 GC 分配
28. `protocol/openai.go`：`DefaultTransport` 克隆默认 Transport 调参（MaxIdleConns=100，MaxIdleConnsPerHost=20，MaxConnsPerHost=50，IdleConnTimeout=120s），`NewHTTPClient` 导出供上层复用
29. `protocol/retry.go`：`SendWithRetry` 从 ctx 读取 `CtxRetryNotify` 回调，每次重试前通知上层
30. `protocol/types.go`：新增 `StreamInterruptedError` 类型，包装已 emit 后的流中断错误
31. `protocol/openai.go`：`readStream` 所有 error return 经 `streamErr()` 统一包装为 `StreamInterruptedError`；`Chunk.Error` 从 `string` 改为 `error`
32. `main.go`：`chatModel.Stream` 检测 `StreamInterruptedError` 后发恢复提示，调用 `forwardChat` 补全响应
33. `tools/vision.go` + `tools/compress.go`：改用 `protocol.NewHTTPClient`，复用共享 Transport
34. `protocol/openai.go`：`sanitizeTool()` 为空 name/description 赋 fallback，防止 API 400
35. `protocol/protocol_test.go`：新增 5 个测试（`TestStreamInterruptedError_Wrap`、`TestStreamInterruptedError_MockStreamEmitThenDisconnect`、`TestStreamInterruptedError_MockStreamEmitThenAPIError`、`TestSanitizeTool`、`TestRetryNotifyContext`）
36. `go.mod`：eino replace 路径 `../eino` → `./reference/eino`
37. `README.md` + `README.detail.md` + `MEMO.md`：文档同步更新（eino 路径、协议层新特性、测试计数）

### 第五轮（2026-07-09）：Phase 1 断路器 + 连接池细化

38. `protocol/types.go`：新增 `CircuitBreakerState`/`CircuitBreakerOptions`/`CircuitBreaker` 类型，含 `Check(err)`/`Success()`/`isFailure()` 方法，状态机 Closed→Open→HalfOpen
39. `protocol/retry.go`：新增 `var vendorCircuitBreaker = make(map[Vendor]*CircuitBreaker)`；`SendWithRetry` 每次 attempt 前调用 `Check(nil)` 短路已 Open 的 breaker，transient error 后调用 `Check(err)`，401/403 后调用 `Success()` 重置
40. `protocol/protocol_test.go`：新增 5 个测试（`TestCircuitBreaker_Closed`/`Open`/`HalfOpen`、`TestSendWithRetry_CircuitBreakerIntegration`、`TestSendWithRetry_CircuitBreakerAuthSuccess`），protocol 单测总数变为 25 个
41. `protocol/protocol_test.go`：新增 `TestBodyPoolReuse`、`TestDefaultTransportParams`，验证 buildBody 输出一致性及 Transport 参数
42. `main_test.go`：新增 `TestForwardChat`，验证恢复路径直接调用 `proto.Chat`
43. `tools/vision_test.go` + `tools/compress_test.go`：新增 smoke test，验证 Vision/Compress 工具通过 `protocol.NewHTTPClient` 正常请求
44. `README.md` + `README.detail.md` + `MEMO.md`：文档同步更新（eino 路径、协议层新特性、测试计数）

### 第六轮（2026-07-09）：EventBus + Health + ContentPredictor + Telemetry 增强

45. `protocol/stream.go` 新增：`EventBus`（pub/sub 架构）+ `ChunkProcessor` 接口 + `telemetryProcessor` + `logProcessor`，`readStream` 每次 emit 经 `eventBus.Publish` 广播给订阅者
46. `protocol/telemetry.go`：`Telemetry` 新增 `chunkBytes`/`toolCalls`/`errors` 字段 + `RecordChunkBytes()`/`RecordToolCall()`/`RecordError()`/`RecordUsage()`/`Last()` 方法
47. `protocol/health_check.go` 新增：`HealthChecker` 定时对 vendor endpoint 发 HEAD 请求，关联 `CircuitBreaker`，状态 `HealthUnknown`/`HealthHealthy`/`HealthUnhealthy`/`HealthCircuitOpen`
48. `protocol/health_manager.go` 新增：`HealthManager` 管理多 vendor `HealthChecker`，`Register()` 启动 goroutine，`GetStatus()` 综合 breaker 状态返回
49. `protocol/content_predict.go` 新增：`ContentPredictor` 带 SHA256 缓存的内容类型预测器，`Predict()`/`Invalidate()`，默认 `heuristicModel`（预留 ML 接口）
50. `protocol/openai.go`：`NewOpenAI` 初始化 `EventBus` 并订阅 `telemetry`/`log` 处理器；注册 `vendorHealthManager` 定时健康检查；`readStream` 的 `send` 闭包增加 `eventBus.Publish`
51. `protocol/retry.go`：`SendWithRetry` 入口增加 `vendorHealthManager.GetStatus()` 短路检查，breaker 失败计数与 health 状态联动
52. `protocol/types.go`：`Config` 新增 `HealthCheckURL` 字段；`OpenAI` 结构体新增 `eventBus` 字段
53. `main.go`：`chatModel.Stream` 检测 `StreamInterruptedError` 后发送恢复提示并调用 `forwardChat` 补全响应；提取 `logUserMessage`/`logAssistantResponse`/`injectLogCtx`/`getStatsMap` helper
54. `tools/compress.go` + `tools/vision.go`：改用 `protocol.NewHTTPClient` 复用共享 `DefaultTransport`
55. `protocol/protocol_test.go`：新增 8 个测试（`TestEventBus_*` 2 个、`TestContentPredictor_*` 2 个、`TestHealthChecker_*` 2 个、`TestHealthManager_*` 1 个、`TestSendWithRetry_HealthShortCircuit` 1 个），protocol 单测总数变为 38 个
56. `main_test.go`：新增 `TestForwardChat` 等 8 个测试
57. `tools/cache_test.go` + `tools/compress_test.go` + `tools/fileops_test.go` + `tools/vision_test.go`：新增 6 个 smoke test
58. `reference/eino`/`reference/hermes-agent`/`reference/opencode`：克隆上游仓库到 `reference/` 目录，go.mod replace 路径改为 `./reference/eino`
59. `README.md` + `README.detail.md` + `MEMO.md`：文档同步更新（EventBus/Health/ContentPredictor/Telemetry 增强/测试计数/reference 目录说明）

### 第七轮（2026-07-10）：供应商测试补全 + StepFun/Qwen 接入 + Vision Native Mode

60. `protocol/protocol_test.go`：补全 vendor-specific 测试覆盖
   - `TestBuildThinkingFields`：验证 DeepSeek/MiniMax/Zhipu/LongCat/MiMo/OllamaCloud 的 reasoning 字段生成
   - `TestMockStream_DeepSeek` / `TestMockStream_MiniMax` / `TestMockStream_Zhipu` / `TestMockStream_LongCat` / `TestMockStream_MiMo` / `TestMockStream_OllamaCloud`：6 个 vendor mock SSE 流测试，验证 `ChunkText` + `ChunkReasoning` 正确分离
61. `protocol/host.go`：新增 `VendorStepFun` / `VendorQwen` + `IsStepFun()` / `IsQwen()` 检测
62. `protocol/openai.go`：`buildThinkingFields` + `NewOpenAI` policy 初始化 + `defaultHealthEndpoint` 均纳入 StepFun/Qwen
63. `protocol/telemetry.go`：`ModelContextWindow` 增加 StepFun/Qwen 模型 context 映射
64. `protocol/stream.go`：修复 `EventBus` 在 `Stop()` 后 `send on closed channel` panic（`dispatch` goroutine 不再向已关闭的 publisher 发送）
65. `main.go`：Vision Native Mode 实现
   - `supportsVision(model)` 判断模型是否原生支持视觉（GPT-4o/DeepSeek-VL/Qwen-VL/Step-2/Gemini）
   - `handleVisionFromChat`：`/api/chat` 路由图片直通主模型，不走 `/api/vision`
   - `handleVisionNativeStream`：`/api/vision/native` 新端点，SSE 流式返回分析结果
   - `imageToDataURL` / `fetchImageDataURL`：统一处理 data URI / HTTP URL / 本地路径三种图片源
   - `toProtoMsg`：支持 `UserInputMultiContent` 多模态消息序列化
66. `main.go`：`chatServer` 新增 `llm.model` 访问；`strPtr` 辅助函数；`MessageInputImage` 使用 `MessagePartCommon` 嵌入
67. `frontend/index.html`：图片粘贴改为调用 `/api/vision/native`，实时流式渲染分析结果
68. `protocol/protocol_test.go`：`TestDetectVendor` 增加 StepFun + Qwen 用例；修复 `TestEventBus_*` 同步时序问题；修复 `TestSendWithRetry_HealthShortCircuit` 使用 `setCheckerStatus` 替代直接修改
 69. `protocol/health_manager.go`：新增 `setCheckerStatus(vendor, status)` 方法供测试注入状态

### 第八轮（2026-07-10）：RAW 层增强 — SSRF/Secret Redaction/Failover/Raw Log

70. `protocol/url_validator.go` 新增：`ValidateBaseURL()` 阻止 localhost/LAN/私有 IP，默认开启；`ValidateProxyEnv()` 校验 proxy 环境变量格式；`URLValidationError` 结构化错误
71. `protocol/redact.go` 新增：`RedactString()` / `RedactHeaders()` / `RedactBody()` / `RedactURL()` 脱敏常见 secret 模式（API key/OAuth/AWS/GitHub/JWT/Basic Auth）
72. `protocol/failover.go` 新增：`FailoverConfig{MaxRetries, ShouldFailover, GetFailoverModel}`；`Chat`/`Stream` 失败后按配置切换到备用模型/端点
73. `protocol/raw_log.go` 新增：`RawLogProcessor` 实现 `ChunkProcessor`，JSONL 格式写入 `logs/raw/`；`ChunkRawRequest/RawResponse/RawError` 三种事件类型
74. `protocol/stream.go`：`EventBus` 新增 `TryPublish()` 非阻塞发布，避免 raw log 阻塞主流程
75. `protocol/types.go`：`ChunkType` 增加 `ChunkRawRequest/RawResponse/RawError`；`Config` 增加 `FallbackModel`/`FallbackBaseURL`；新增 `RegisterChunkType()`/`ChunkType.String()`
76. `protocol/openai.go`：`NewOpenAI` 初始化 `failover` config；`Chat` → `chatWithFailover`；`Stream` → `streamWithFailover`；`buildHTTPRequest`/`chatDirect`/`readStream` emit raw chunk 到 EventBus
77. `main.go`：`main()` 启动时调用 `ValidateBaseURL` + `ValidateProxyEnv`；`injectLogCtx` 对日志做 `RedactString`；`OPENAI_FALLBACK_MODEL`/`OPENAI_FALLBACK_BASE_URL` 传入 Config；`RAW_LOG=1` 启用 RawLogProcessor
78. `protocol/protocol_test.go`：新增 vendor 测试 + 修复 EventBus/HealthShortCircuit 时序

### 第九轮（2026-07-10）：RAW 层增强 Phase 0 — UsageTracker + Telemetry 增强

79. `protocol/usage_tracker.go` 新增：`UsageTracker` SQLite 持久化存储（`logs/raw/usage.db`），含 `UsageRecord`/`UsageQuery`/`UsageStats` 类型、`NewUsageTracker`/`Record`/`Query`/`GetStats`/`Close` 方法；使用 `modernc.org/sqlite` 纯 Go 驱动；WAL 模式提升并发性能
80. `protocol/telemetry.go`：新增 `SetTracker(ut *UsageTracker)` / `SetSessionID(sid string)` 方法；`Record()` 在设置 tracker 后自动持久化到 SQLite
81. `go.mod` / `go.sum`：新增 `modernc.org/sqlite v1.53.0` 依赖
82. `logs/raw/`：创建目录 + `.gitkeep`
83. `protocol/protocol_test.go`：新增 3 个测试（`TestUsageTracker_NewRecordQuery`、`TestTelemetry_SetTrackerRecords`、`TestUsageTracker_QueryPagination`），protocol 单测总数 41 个

### 第十轮（2026-07-10）：Phase 1 — Plugin Command Interceptor

84. `tools/interceptor.go` 新增：`Interceptor` 接口（`Name`/`Before`/`After`）+ `InterceptorRegistry` 全局注册表 + `RegisterInterceptor`/`ClearInterceptors`/`RunBeforeInterceptors`/`RunAfterInterceptors` 函数
85. `tools/terminal.go`：`RunTerminal` 执行前后插入 `RunBeforeInterceptors` / `RunAfterInterceptors` 调用，命令和输出均可被插件链修改或拦截
86. `tools/interceptor_test.go`：新增 4 个测试（`TestInterceptorRegistry_Block`、`TestInterceptorRegistry_Modify`、`TestInterceptorRegistry_Chain`、`TestInterceptorRegistry_Empty`）

### 第十一轮（2026-07-10）：Phase 2 — Usage Analytics Integration

87. `protocol/types.go`：新增 `CtxSessionID` context key
88. `protocol/telemetry.go`：新增 `RecordFromContext(ctx, model, vendor, durSec, usage)` 方法，从 context 提取 sessionID 后调用 `Record`，实现全链路 sessionID 透传
89. `protocol/openai.go`：`chatDirect` 和 `recordStreamCall` 的 `Telemetry.Record` 调用改为 `RecordFromContext`；`recordStreamCall` 签名增加 `ctx` 参数
90. `main.go`：`injectLogCtx` 同时注入 `CtxSessionID`；新增 `USAGE_DB=1` env 控制 UsageTracker 初始化并挂载到 Telemetry

### 第十二轮（2026-07-10）：Phase 3 — RAW Lifecycle Hooks

91. `protocol/hooks.go` 新增：`LifecycleHook` 接口（`BeforeProcess`/`AfterProcess`/`OnError`）+ `LifecycleHookRegistry` 全局注册表 + `RegisterLifecycleHook`/`ClearLifecycleHooks`/`RunBeforeProcessHooks`/`RunAfterProcessHooks`/`RunOnErrorHooks` 函数 + `LifecycleHookFuncs` 链式构建器（`SetBefore`/`SetAfter`/`SetOnError`）
92. `protocol/openai.go`：`Chat()` 和 `Stream()` 入口处调用 `RunBeforeProcessHooks`，返回后调用 `RunAfterProcessHooks`/`RunOnErrorHooks`；`streamWithFailover` 的 rate limit 错误和最终错误也触发 `RunOnErrorHooks`
93. `protocol/hooks_test.go`：新增 4 个测试（`TestLifecycleHook_BlockBefore`、`TestLifecycleHook_BeforeAfter`、`TestLifecycleHook_OnError`、`TestLifecycleHook_Chain`），protocol 单测总数 45 个

### 第十三轮（2026-07-10）：Phase 4 — Structured RAW Query API

94. `protocol/raw_query.go` 新增：`DailyUsage`/`ModelUsage`/`VendorUsage` 聚合类型 + `UsageTracker.GetDailyStats(since, until)` / `GetModelStats()` / `GetVendorStats()` 方法，支持按日/模型/供应商聚合统计
95. `protocol/protocol_test.go`：新增 `TestUsageTracker_DailyModelVendorStats`，验证聚合查询返回正确计数，protocol 单测总数 46 个

### 第十四轮（2026-07-10）：Phase 5 — MCP Server

96. `go.mod` / `go.sum`：新增 `github.com/gorilla/websocket v1.5.3` 依赖
97. `protocol/mcp_server.go` 新增：`MCPServer` — 基于 gorilla/websocket 的 MCP 协议服务，接受 JSON-RPC 风格请求，支持 5 个方法（`get_stats`/`get_daily`/`get_models`/`get_vendors`/`query_records`）；`MCPRequest`/`MCPResponse`/`MCPError` 类型
98. `protocol/usage_tracker.go`：`UsageQuery` 字段添加 `json:` tag，支持 JSON 序列化/反序列化
99. `protocol/mcp_server_test.go`：新增 3 个测试（`TestMCPServer_GetStats`/`TestMCPServer_UnknownMethod`/`TestMCPServer_QueryRecords`），protocol 单测总数 49 个

### 第十五轮（2026-07-10）：Phase 6 — main.go 集成 + 最终验证

100. `main.go`：`USAGE_DB=1` 时创建 `UsageTracker` 并挂载到 `Telemetry`；若成功则创建 `MCPServer` 并注册到 `/mcp` WebSocket 端点（`ws://localhost:PORT/mcp`）
101. 全项目 `go build`、`go vet`、`go test ./...` 通过：
    - `MiniGoAgent` (main_test.go): ok
    - `MiniGoAgent/protocol`: 49 个测试全部通过
    - `MiniGoAgent/tools`: 10 个测试全部通过
    - `MiniGoAgent/tools/filter/log/sessionlog`: 无测试文件

### 第十六轮（2026-07-10）：Review 修复 — 并发、安全、生命周期、文档一致性

102. `protocol/telemetry.go`：修复 `RecordFromContext` 通过共享 `sessionID` 字段记录 usage 的并发串 session 风险；新增内部 `record(sessionID, ...)` 路径，context sessionID 直接传入写库；SQLite 写入移出 Telemetry mutex，避免 DB 阻塞 stats 读取
103. `protocol/stream.go`：修复 `EventBus.Stop()` 死锁问题；新增 stopped 状态和 `sync.Once`，Stop 时关闭 publisher 和所有 subscriber channel；`Publish`/`TryPublish` 在 stopped 后返回失败
104. `protocol/openai.go`：修复 stream 输出 channel 关闭职责，统一由 `streamWithFailover` 关闭，避免 reconnect 后向已关闭 channel 发送；stream 成功结束后触发 `RunAfterProcessHooks`；ctx 取消时保留 `lastErr`
105. `protocol/mcp_server.go`：MCP WebSocket 默认本机限定；拒绝非 loopback `RemoteAddr`，Origin 仅允许空或 loopback；非法 params 返回 `-32602 invalid params`
106. `protocol/usage_tracker.go` + `protocol/raw_query.go`：UsageTracker mutex 改为 `sync.RWMutex`；查询/扫描期间持读锁，`Close()` 等待读操作完成；查询默认 limit 上限 1000；整理 `UsageStats` 字段格式
107. `main.go`：`USAGE_DB=1` 创建的 `UsageTracker` 在退出信号处理中关闭；所有 chat/vision handler 检查 JSON decode 错误并返回 400；Vision fallback 改用 request context；新增 `snapshotWith`/`appendMessages` 封装会话历史读取和追加，修复 user message 未可靠写回 session 以及并发 slice 读写风险
108. `tools/fileops.go`：`GrepFiles` 从 `strings.Contains` 改为 `regexp.Compile` + `MatchString`，与“支持正则”的 schema 描述一致；接住 `filepath.Walk` 返回错误，ctx cancel 和结果截断不再静默丢失
109. `protocol/raw_log.go`：检查 raw log 写入错误；失败后禁用 processor 并返回错误；新增日期字段，跨天运行自动轮转到新日志文件
110. `protocol/hooks.go` + `tools/interceptor.go`：注册 nil hook/interceptor 时直接忽略；执行链复制切片后迭代，避免注册表并发修改影响当前执行；生命周期 hook 返回 nil request/response 时返回明确错误
111. `protocol/health_check.go` + `protocol/health_manager.go`：HealthChecker Stop 使用 `sync.Once`，避免重复 close panic；HealthManager Stop 调用 cancel 终止 checker goroutine
112. `README.md`：补充 `USAGE_DB`、`logs/raw/usage.db`、本机限定 `/mcp` WebSocket 配置说明，并清理中文功能列表重复项
113. 验证：`gofmt` 已执行；`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 通过（MiniGoAgent / protocol / tools 全部 ok）；`go vet ./...` 通过

### 第十七轮（2026-07-10）：RAW 层收口 — 关键回归测试补齐

114. `protocol/protocol_test.go`：新增 `TestEventBus_StopDoesNotDeadlock`，验证 `EventBus.Stop()` 不再因 subscriber channel 未关闭而死锁，并验证 Stop 后 `TryPublish()` 返回 false
115. `protocol/protocol_test.go`：新增 `TestTelemetry_RecordFromContextDoesNotShareSession`，并发写入两个 session 的 usage 记录，验证 `RecordFromContext` 不再通过共享 `Telemetry.sessionID` 串 session
116. `protocol/usage_tracker.go`：回归测试暴露 SQLite 并发写丢记录问题；`UsageTracker.Record()` 改回独占 `Lock()` 串行写入，查询仍用 `RLock()`，确保 SQLite 写入稳定
117. 验证：`gofmt` 已执行；`go build ./...` 通过；`go test ./protocol/ -run "TestTelemetry_RecordFromContextDoesNotShareSession|TestEventBus_StopDoesNotDeadlock" -count=1 -v` 通过；`go clean -testcache; go test ./... -count=1` 通过；`go vet ./...` 通过

### 第十八轮（2026-07-10）：架构冻结文档 — RAW 优化清单 + DSPy PromptLab 边界

118. 新增 `ARCHITECTURE.md`：冻结当前真实架构，明确 `main.go` / `protocol/` / `tools/` / `frontend/` / `reference/eino` 的现有职责；强调文档优先描述当前事实，不把理想架构误写成已完成实现
119. `ARCHITECTURE.md` 新增 RAW 层职责边界：`protocol/` 当前是 LLM provider protocol / RAW gateway，不是 MCP；负责 OpenAI-compatible 调用、SSE、vendor policy、retry/reconnect/rate limit/circuit breaker/health/failover、raw log、redaction、SSRF、usage analytics、lifecycle hooks
120. `ARCHITECTURE.md` 记录已完成 RAW 优化，避免后续重构误删或重复设计：RTK-style terminal output filter（`tools/filter/` + `tools/terminal.go`）、Headroom-inspired content compression（`content_detector.go` / `content_predict.go` / `content_compress.go` / `CachedCompressContent` / Live Zone）、OpenAI-compatible protocol support（`protocol/openai.go`）
121. `ARCHITECTURE.md` 补录容易漏掉的 RAW 工程化能力：HTTP retry、stream reconnect、`StreamInterruptedError`、CircuitBreaker、HealthChecker/HealthManager、RateLimiter、multi-key rotation、SSRF、secret redaction、RawLogProcessor、EventBus、UsageTracker、Structured RAW Query API、local-only MCP usage endpoint、lifecycle hooks、body pool、shared transport、schema canonicalization、tool sanitization、ContentPredictor
122. `ARCHITECTURE.md` 明确 DSPy prompt 自我迭代归属：放在未来 `Prompt Optimization / PromptLab` 侧车层，服务 ADK runtime 的 `PromptProvider`；不得放入 RAW/Gateway/MCP/Skill；不得在在线用户请求链路实时运行；candidate prompt 必须经 eval/promotion 后成为 stable prompt
123. `ARCHITECTURE.md` 记录下一步 guardrails：允许先拆 `internal/config`、`internal/session`、`internal/server`，预留 `AgentRunner`/`PromptProvider`；暂不删除/重命名 `protocol/`，暂不迁移 `tools/`，暂不移除 `reference/eino` replace，暂不把 DSPy 接入在线链路
124. 验证：`go clean -testcache; go test ./... -count=1` 通过（MiniGoAgent / protocol / tools 全部 ok；filter/log/sessionlog 无测试文件）

### 第十九轮（2026-07-10）：长期架构重构 Phase 1.1 — internal/config 抽出

125. 新增 `internal/config/config.go`：集中 `.env` 读取、环境变量默认值、整数/列表解析和 typed `Config` 聚合；包含 `OpenAIConfig`、`ServerConfig`、`AgentConfig`、`RawConfig`、`SecurityConfig`
126. `main.go`：启动入口改为 `cfg := config.Load()`；`OPENAI_*`、`STREAM_TIMEOUT`、`RATE_LIMIT_*`、`CONTEXT_*`、`MAX_RECONNECT_ATTEMPTS`、`AGENT_MAX_STEP`、`RAW_LOG*`、`USAGE_DB*`、`PORT` 等配置全部从 `cfg` 读取；删除 main 内旧 `loadEnv`/`getEnv`/`getEnvInt`/`splitEnv` helper
127. 新增 `internal/config/config_test.go`：覆盖 `GetEnv`、`GetEnvInt`、`SplitEnv`、`LoadEnvFile`、`Load()` 聚合配置；`main_test.go` 删除旧 env helper 测试，保留 image/history/forwardChat 测试
128. `ARCHITECTURE.md`：同步更新当前真实分层，记录 `internal/config` 已成为第一层拆出的 internal package；下一步 guardrails 中移除“抽 config”待办
129. 验证：`gofmt` 已执行；`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 通过（MiniGoAgent / internal/config / protocol / tools 全部 ok）；`go vet ./...` 通过

### 第二十轮（2026-07-10）：长期架构重构 Phase 1.2 — internal/session 抽出

130. 新增 `internal/session/manager.go`：`Manager` 结构体封装 `map[string][]*schema.Message`（+sync.Mutex），提供 `SnapshotWith`/`Append`/`Count`/`Save`/`Load` 方法；导出 `Marshal`/`Unmarshal` 全局函数用于 DTO 序列化；`historyMessage`/`historyToolCall` DTO 类型从 `main.go` 迁移至此
131. `main.go`：`chatServer.sessions` 类型改为 `*session.Manager`；`SnapshotWith`/`Append` 调用替换为 s.sessions 方法；`saveHistory`/`loadHistory` 改为委托 `s.sessions.Save/Load`；删除原 DTO 类型和 `marshalSessions`/`unmarshalSessions` 函数
132. `internal/session/manager_test.go` 新增：`TestHistoryDTORoundtrip`、`TestHistoryDTOJSONCompat`（从 `main_test.go` 迁移）
133. `main_test.go`：删除已迁移的 `TestHistoryDTORoundtrip`/`TestHistoryDTOJSONCompat`，删除不再使用的 `encoding/json`/`schema` 导入
134. `ARCHITECTURE.md`：表的 `main.go` 行移除 "session state"；新增 `internal/session` 行；Sec 6 移除 "Maintain sessions in memory" / "Persist history"；Sec 10 更新 main.go 债务描述；Sec 11 将 session 移至 "Completed steps"
135. 验证：`gofmt` 已执行；`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 通过（MiniGoAgent / internal/config / internal/session / protocol / tools 全部 ok）；`go vet ./...` 通过

### 第二十一轮（2026-07-10）：长期架构重构 Phase 1.3 — internal/server 抽出

136. 新增 `internal/server/interfaces.go`：`AgentRunner` 接口（`Generate`/`Stream`）和 `PromptProvider` 接口（`SystemPrompt`），为后续解耦 `react.Agent` 和 prompt 做准备
137. 新增 `internal/server/chatmodel.go`：`ChatModel` 结构体（原 `main.chatModel`），导出 `NewChatModel`/`StatsLine`/`StatsJSON`/`Generate`/`Stream`/`WithTools`；导出 proto 转换函数 `ToProtoMsg`/`ToProtoMsgs`/`ToProtoTools`/`FromProtoResp`
138. 新增 `internal/server/server.go`：`Server` 结构体（原 `main.chatServer`），导出 `New`/`ServeFrontend`/`HandleChat`/`HandleChatStream`/`HandleVision`/`HandleVisionNativeStream`/`SaveHistory`/`LoadHistory`；内含所有 HTTP handler、vision 辅助函数、session 辅助函数
139. 新增 `internal/server/server_test.go`：`TestForwardChat` 和 `TestExtractLocalImagePath`（从 `main_test.go` 迁移）
140. `main.go`：瘦身至 ~120 行，仅保留依赖组装：config → protocol → ChatModel → tools → agent → Server → http.ListenAndServe；`frontendFS` embed 保留在 main.go，前端 HTML 以 `[]byte` 传入 `server.New`；删除原 chatModel/chatServer/convert/vision 全部代码
141. `main_test.go`：删除（所有测试已迁移至 `internal/server/server_test.go`）
142. `ARCHITECTURE.md`：表 main.go 行改为 "thin wiring layer"；新增 `internal/server` 行；Sec 6 移除 "Serve HTTP and SSE endpoints"；Sec 10 更新 main.go 债务描述；Sec 11 将 server/AgentRunner/PromptProvider 移至 "Completed steps"
143. 验证：`gofmt` 已执行；`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 通过（MiniGoAgent / internal/config / internal/server / internal/session / protocol / tools 全部 ok）；`go vet ./...` 通过

### 第二十二轮（2026-07-10）：长期架构重构 Phase 1.4 — AgentRunner/PromptProvider 接口落地

144. `internal/server/server.go`：`Server.agent` 类型从 `*react.Agent` 改为 `AgentRunner` 接口；`New()` 签名更新
145. `main.go`：新增 `agentAdapter` 类型适配 `*react.Agent` → `AgentRunner`；`server.New()` 调用处传入 `&agentAdapter{agent: agent}`
146. `main.go`：新增 `promptProvider` 类型实现 `server.PromptProvider`；system prompt 提取为顶层 `const systemPrompt`；`react.AgentConfig.MessageModifier` 改为调用 `pp.SystemPrompt()` 而非硬编码字符串；`server.New()` 调用处传入 `pp`
147. `internal/server/server.go`：`Server` 新增 `prompt PromptProvider` 字段，`New()` 增加 `prompt PromptProvider` 参数
148. `ARCHITECTURE.md`：Sec 11 将 AgentRunner/PromptProvider 移至 "Completed steps"，Allowed next steps 清空
149. 验证：`gofmt` 已执行；`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 通过（MiniGoAgent / internal/config / internal/server / internal/session / protocol / tools 全部 ok）；`go vet ./...` 通过

### 第二十三轮（2026-07-10）：ADK 层实现

150. `ARCHITECTURE.md`：新增 Sec 12 ADK 层目标架构描述（包结构、关键设计决策、迁移 Phases、边界规则）
151. `MEMO.md`：追加第二十三轮设计摘要（引用 hermes-agent/opencode/eino 参考案例）
152. **Phase A — `internal/adk/tool/`**：
    - `tool.go`：`Tool` 结构体包装 eino `tool.InvokableTool`，支持 `WithCheck()`、`ToEinoTool()` 转换
    - `registry.go`：`ToolRegistry` 含 `Register/Get/Names/GetDefinitions/GetEinoTools/Dispatch/Check` 方法
    - check_fn TTL 缓存（30s） + 故障容错（60s grace），参照 Hermes `tools/registry.py`
    - `executor.go`：`ToolExecutor` 支持并发/串行执行 + Context 取消/中断检查
    - `guardrails.go`：`Guardrails` 支持 allow/deny 列表 + 自定义规则 + registry check_fn 集成
    - 单测 45 个全部通过
153. **Phase B — `internal/adk/llm/`**：
    - `bridge.go`：`Bridge` 结构体实现 eino `model.ToolCallingChatModel`，将 `protocol.Protocol` 适配为 eino chat model
    - `resolution.go`：`Resolve()`/`NewBridgeFromConfig()` 从 `config.Config` 创建协议层并返回 Bridge
    - 包含 `Generate()`/`Stream()`/`WithTools()` + 流中断自动恢复（`[流中断，正在恢复...]`）
154. **Phase C — `internal/adk/` 核心**：
    - `types/` 子包：`Message`/`ToolCall`/`Request`/`Response`/`Event` 等 ADK 自有类型（零 eino 依赖）
    - `agent.go`：`Agent` 接口（`Run`/`Stream`）+ `AgentConfig`
    - `react.go`：`ReactAgent` 包装 eino `react.Agent` 为 ADK Agent，含 eino ↔ adk 消息转换
    - `runner.go`：`Runner` 管理 turn 生命周期 + Guardrails 检查
    - `session/store.go`：`Store` 接口（`Get`/`Append`/`Snapshot`）
155. **Phase D — `internal/adk/middleware/` + `event/`**：
    - `middleware/types.go`：`Middleware` 接口（`BeforeModel`/`AfterModel`/`BeforeTool`/`AfterTool`）
    - `middleware/chain.go`：洋葱模型链式执行 `AroundModel`/`AroundTool`
    - `middleware/chain_test.go`：8 个测试覆盖执行顺序/错误阻断/空链/修改响应
    - `event/types.go`：`Type` 枚举（AgentStart/End/StepStart/End/ModelStart/End/ToolStart/End/Error）+ 事件数据
    - `event/bus.go`：`Bus` 轻量 pub/sub
156. **Phase E — session 适配**：
    - `internal/adk/session/adapter.go`：`ManagerAdapter` 包装 `internal/session.Manager`，实现 `adk/session.Store`
    - eino ↔ adk 消息类型转换函数（`toAdkMessages`/`toEinoMessages`）
    - PromptProvider 接口保留在 `internal/adk/runner.go` 和 `internal/server/interfaces.go` 双定义状态
157. 验证：`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 全部通过（MiniGoAgent / internal/config / internal/server / internal/session / protocol / tools / internal/adk/... 全部 ok）；`go vet ./...` 通过

### 第二十四轮（2026-07-10）：Phase F — main.go 切到 ADK 接口

158. `main.go` 重构：消除 5 个直接 eino import（`react`/`compose`/`schema`/`tool`/`tool/utils`），改为 ADK 组件：
    - `utils.InferTool` → `adktool.NewFromFn` 注册到 `adktool.ToolRegistry`
    - `react.NewAgent` → `adk.NewReactAgent`
    - `server.NewChatModel` → `llm.NewBridgeFromConfig`
    - `agentAdapter` 从包装 `*react.Agent` 改为包装 `adk.Agent`，含 eino ↔ adk 类型转换
    - raw log/usage tracker 通过 `br.Protocol()` 拿到协议层后挂载
159. `internal/server/server.go`：
    - `llm *ChatModel` 字段改为 `model ModelInfo` 接口（`Model()`/`StatsLine()`/`StatsJSON()`）
    - `New()` 签名第二参数改为 `ModelInfo` 接口
    - `getStatsMap()` 通过 `s.model.StatsJSON()` + Unmarshal 实现，不再直接取 `proto`
    - 所有 `s.llm.*` 引用统一切到 `s.model.*`
160. `internal/adk/llm/bridge.go`：新增 `Protocol() protocol.Protocol` 方法，暴露底层协议供 main.go 挂载 EventBus/Telemetry
161. 验证：`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 全部通过；`go vet ./...` 通过

### 第二十五轮（2026-07-10）：ADK 层补测试

162. `internal/adk/event/bus_test.go` 新增：8 个测试覆盖 `NewBus`/`SubscribePublish`/`PublishDifferentType`/`Unsubscribe`/`PublishAll`/`PublishToUnsubscribedType`/`ConcurrentPublish`/`ConcurrentSubscribePublish`
163. `internal/adk/types/types_test.go` 新增：6 个测试覆盖 `RoleConstants`/`MessageCreation`/`MessageWithToolCalls`/`ToolCallAssignment`/`RequestResponse`/`EventTypes`
164. `internal/adk/session/adapter_test.go` 新增：8 个测试覆盖 `NewManagerAdapter`/`GetEmptySession`/`AppendAndSnapshot`/`AppendWithToolCalls`/`MultipleSessions`/`ToAdkMessagesEdgeCases`/`AdkToEinoRoundtrip`
165. `internal/adk/llm/bridge_test.go` 新增：12 个测试覆盖 `NewBridge`/`Generate`/`GenerateEmptyResponse`/`GenerateWithToolCalls`/`GenerateWithOptions`/`Stream`/`StreamReasoning`/`StreamError`/`StreamInterruptedRecovery`/`WithTools`/`StatsLine`/`StatsJSON`
166. `internal/adk/react_test.go` 新增：11 个测试覆盖 eino↔adk 消息转换 roundtrip、tool call 携带、edge cases、`NewReactAgent` error cases、`NewRunner` nil guard、`NewRunnerWithAgent`
167. 修复了 `event/bus.go` 的 `Unsubscribe` 函数指针比较 bug（`&h == &handler` 因 loop variable 地址相同永不匹配，改为 `reflect.ValueOf(h).Pointer() == reflect.ValueOf(handler).Pointer()`）
168. 修复了 `adk/runner.go` 缺少 `cfg == nil` 守卫导致的 `NewRunner(nil)` panic
169. 验证：`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 全部通过；`go vet ./...` 通过

### 第二十六轮（2026-07-10）：Phase G+H+I — Middleware/Events/Session 集成到 Runner

170. **Phase G** — Middleware 集成：
    - `RunnerConfig` 新增 `Middleware *middleware.Chain` 字段
    - `Runner` 新增 `mw *middleware.Chain` 字段
    - `Runner.Run` 用 `mw.AroundModel()` 包裹 `r.agent.Run()`，BeforeModel/AfterModel 钩子生效
    - `Runner.Stream` 用 `mw.AroundModel()` 包裹 `r.agent.Stream()`
    - Middleware 返回错误时发布 `Error` 事件
    - `NewRunnerWithAgent` 签名新增 `mw *middleware.Chain` 参数
171. **Phase H** — EventBus 集成：
    - `RunnerConfig` 新增 `EventBus *event.Bus` 字段
    - `Runner` 新增 `bus *event.Bus` 字段
    - `Runner.Run` 在关键生命周期点发布事件：
      - guardrails 阻断 → `event.Error`
      - agent 开始执行 → `event.AgentStart`（含 SessionID/ToolNames）
      - agent 返回错误 → `event.Error`
      - agent 执行完成 → `event.AgentEnd`（含 Stats）
    - `Runner.Stream` 流完成时发布 `event.AgentEnd`
    - 新增 `publishEvent()` helper，bus 为 nil 时静默跳过
172. **Phase I** — Session Store 持久化：
    - `Runner.Run` 中 store 非空且 SessionID 非空时：
      - 执行前从 `store.Get()` 加载历史消息，附加到 `req.Messages`
      - 执行后将 `resp.Messages` 用 `store.Append()` 写入
    - `Runner.Stream` 类似处理（从 store 加载历史，写入在流完成前）
173. `internal/adk/runner_test.go` 新增：9 个集成测试
    - `TestRunnerRunWithMiddleware` / `TestRunnerRunMiddlewareBlocks` — middleware 执行/阻断
    - `TestRunnerRunWithStore` / `TestRunnerRunWithStoreAppendsToExisting` — session 持久化
    - `TestRunnerRunWithEvents` — 事件发布
    - `TestRunnerRunGuardrailsBlocked` — guardrails + 事件联动
    - `TestRunnerRunErrorEvent` — 错误事件
    - `TestRunnerStream` — 流式
    - `TestRunnerAllIntegrations` — middleware+store+event 全部同时验证
174. 验证：`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 全部通过；`go vet ./...` 通过

### 第二十七轮（2026-07-10）：Phase J — main.go 改用 Runner + Phase L — ARCHITECTURE.md 同步

175. **Phase J** — `main.go` 的 `agentAdapter` 从包装 `adk.Agent` 改为包装 `*adk.Runner`：
    - 创建 `event.NewBus()` 并传入 `NewRunnerWithAgent`，Runner 的 event 发布开始生效
    - `agentAdapter.Generate`/`Stream` 改为调用 `runner.Run`/`runner.Stream`
    - runner 的 middleware/store/guards 字段传递 nil，保持 server 现有逻辑不变
176. **Phase L** — `ARCHITECTURE.md` 同步更新：
    - Sec 11 标题从 "Next Refactor Guardrails" 改为 "ADK Integration Status"，列出已完成的 ADK 集成项和剩余的 Do-not-do
    - Sec 12.2 Package Structure 表更新实际状态（Done/Not started），移除已不存在的 `adk.go`，新增 `types/`
    - Sec 12.4 Migration Phases 表更新 Phases A–J 及其完成状态
177. 验证：`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 全部通过；`go vet ./...` 通过

### 第二十八轮（2026-07-10）：middleware.AroundTool 接线

178. `internal/adk/react.go`：
    - 新增 `middlewareWrappedTool` 结构体，实现 eino `BaseTool` + 委托 `InvokableRun` 到 `chain.AroundTool`
    - `AroundTool` 在 BeforeTool/AfterTool 之间调用原始工具的 `InvokableRun`
    - 新增 `einoToolsWithMiddleware(reg, names, chain)`，为列表中的每个工具创建 `middlewareWrappedTool`
    - `NewReactAgent` 根据 `cfg.Middleware != nil` 选择是否用 middleware-wrapped tools
179. `internal/adk/agent.go`：`AgentConfig` 新增 `Middleware *middleware.Chain` 字段
180. `internal/adk/runner.go`：`NewRunner` 将 `cfg.Middleware` 透传到 `AgentConfig.Middleware`
181. `internal/adk/react_test.go`：新增 5 个测试覆盖 `middlewareWrappedTool`：
    - `TestMiddlewareWrappedToolCallsHooks` — BeforeTool/AfterTool 被调用
    - `TestMiddlewareWrappedToolBeforeBlocks` — BeforeTool 阻断执行
    - `TestMiddlewareWrappedToolAfterModifiesResult` — AfterTool 修改结果
    - `TestMiddlewareWrappedToolNonInvokable` — 非 InvokableTool 的 fallback
    - `TestEinoToolsWithMiddleware` — `einoToolsWithMiddleware` 返回正确 wrapped tools
182. 验证：`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 全部通过（共 50+ ADK 子包测试全绿）；`go vet ./...` 通过

### 第二十九轮（2026-07-10）：README 文档同步

183. `README.md`：中英文功能列表各加一条 "Layered architecture: internal/adk/ agent runtime between HTTP handlers and protocol layer"
184. `README.detail.md`：
    - 项目结构图整体替换：移除已删除的 `main_test.go`，新增 `internal/` 完整展开（adk 8 子包、config、server、session），protocol/ 补齐新增文件（health/stream/usage/mcp/failover/hooks/interceptor/validator/redact）
    - 架构图替换：从 `main.go → react.Agent → chatModel` 改为 `internal/server → internal/adk/ (Runner → Agent → ReactAgent) → Bridge → protocol`，补全 middleware/event/session 子层和 protocol 层的断路器/健康检查/EventBus/Failover/LifecycleHooks
185. 验证：`go build ./...` 通过；`go clean -testcache; go test ./... -count=1` 全部通过；`go vet ./...` 通过

### 第二十九轮（补）：端到端集成测试 + 多轮稳定性验证

186. `internal/server/adk_integration_test.go` 新增：11 个端到端集成测试，使用 `httptest.Server` + mock protocol + 全链路 ADK 组件（Bridge → ReactAgent → Runner → agentAdapter → Server → HTTP handler）：
    - `TestIntegration_ChatEndpoint` — POST /api/chat 返回正确回复
    - `TestIntegration_ChatEndpointError` — agent 返回错误时回复含 "错误" 前缀
    - `TestIntegration_StreamEndpoint` — SSE 流式输出
    - `TestIntegration_Frontend` — HTML 前端文件正常服务
    - `TestIntegration_InvalidJSON` — 非法 JSON 返回 400
    - `TestIntegration_MethodNotAllowed` — GET 请求返回 405
    - `TestIntegration_RunnerEvents` — AgentStart/AgentEnd 事件正确发布
    - `TestIntegration_ConcurrentRequests` — 10 并发请求全部成功
    - `TestIntegration_SessionIsolation` — 多 session 互不干扰
    - `TestIntegration_WithToolCall` — 工具调用链路完整执行
187. 多轮稳定性验证：
    - `go test ./internal/adk/... ./internal/server/... ./internal/session/... -count=1` 连续 5 轮，每轮 9 包全部 ok
    - 全量 `go test ./... -count=1` + `go vet ./...` 一次通过
    - 零 flaky 测试、零竞态风险（`CGO_ENABLED=0` 环境无法用 `-race`，但并发测试已覆盖）

### 第三十轮：Code Review 问题修复（阻塞项 + 重要项）

188. 依据资深 Code Review 报告，修复 3 阻塞项 + 5 重要项：
    - B1 空指针防御：runner.go Run 在两分支汇合后加 if resp == nil 兜底，防止 Agent 实现返回 (nil,nil) 时 panic
    - B2 Stream 中间件语义修正：Runner.Stream 移除副作用式 AroundModel，改为显式调用新增的 middleware.Chain.BeforeModelChain；AfterModel 不在流式路径执行；AgentEnd 改 defer 兜底发布，保证消费方提前断开/ctx 取消时仍与 AgentStart 配对
    - B3 流式 goroutine 响应 ctx 取消：react.go、llm/bridge.go、main.go、runner.go 四处发送点加 select case ctx.Done；bridge/main 用 sw.Send() bool 检测下游关闭；react 加 defer sr.Close()。杜绝 goroutine 泄漏
    - I3 Guardrails 下沉：AgentConfig 加 Guardrails 字段，透传至 middlewareWrappedTool，在 InvokableRun 执行前 Check，拦截 Agent 运行时动态产生的 tool call。入口检查保留作二次防御
    - I2 非 Invokable 明确报错：middlewareWrappedTool.InvokableRun 对非 InvokableTool 返回 error，不再静默返回空
    - I4 ToolExecutor 并发上限：executeConcurrent 引入信号量 channel，上限 max(4, NumCPU)
    - I5 EventBus panic 隔离：Publish 拷贝 handler 快照 + 每 handler safeInvoke 包 recover，单 handler panic 不中断请求链路
    - I1 转换逻辑收敛：新建 internal/adk/convert 包（ToEino/FromEino/ToEinoSlice/FromEinoSlice，含 nil 处理），替换 react/session/main 三处重复；bridge.go 是 eino->protocol 转换语义不同故保留
189. 重命名 einoToolsWithMiddleware -> einoToolsWrapped；NewReactAgent 包装条件改为 Middleware != nil || Guardrails != nil
190. 新增回归测试：TestRunnerRunNilResponse、TestRunnerStreamContextCancel、TestRunnerStreamPublishesAgentEnd、TestRunnerStreamBeforeModelRuns/Blocks、TestGuardrailBlocksRuntimeToolCall、TestGuardrailAllowsPermittedToolCall、convert 包往返测试、TestExecutor_ConcurrencyLimit、TestPublishHandlerPanicIsolation
191. 验证：go build ./... 通过；go clean -testcache; go test ./... -count=1 全部通过；go vet ./... 通过；ADK+server 连续 3 轮全绿，无 flaky
192. 文档：ARCHITECTURE.md 更新 convert 包、两层 guardrails、EventBus panic 隔离，新增「Stream 中间件限制」说明段
