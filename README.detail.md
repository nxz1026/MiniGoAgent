# MiniGoAgent

A minimal LLM Agent built with Go and the [eino](https://github.com/cloudwego/eino) framework, featuring Web UI, streaming chat, terminal execution, web search, image recognition, and multi-session conversations.

---

## Features

- **Web UI** — Chat interface with streaming SSE output, markdown rendering, image paste
- **Multi-session** — `?session=<id>` isolates conversation histories
- **Agent Loop** — ReAct agent with up to 12 steps, tool-call pairing repair, message rewriting
- **9 Tools** — Terminal, web search, text compression, file read/write/edit, glob, grep, vision
- **Output Filter** — RTK-style, command-aware filtering (ls/git/general), semantic-safe fallback
- **Result Cache** — 30s TTL for read-only terminal commands
- **History Persistence** — Auto-save on Ctrl+C, restore on startup
- **Image Analysis** — Vision via StepFun API
- **Session Logging** — Per-session `.md` log in `logs/sessions/<id>/`, records user/assistant/tool/raw events
- **Telemetry Card** — Stats displayed inline in chat: `47s · auto · ↑92.7k · ↓1.4k · ctx 92.7k/524k 18%`
- **Raw Layer Logging** — Protocol-level debug via context-injected `CtxLogf`, sessions log raw HTTP interaction

### Protocol Layer (`protocol/`)

- **Vendor Detection** — Auto-detects DeepSeek, Zhipu, MiniMax, LongCat, Ollama Cloud, MiMo from baseURL
- **Reasoning Content** — Runtime detection; emits `ChunkReasoning` events on any provider that returns `reasoning_content`
- **HTTP Retry** — Exponential backoff + jitter, 10 max retries, `Retry-After` support, auth retry
- **Stream Reconnect** — Configurable reconnection attempts (env `MAX_RECONNECT_ATTEMPTS`, default 3) before any token emitted
- **Tool Call Normalization** — Broken JSON repair, orphan tool message removal, missing result backfill, name backfill
- **Think Tag** — MiniMax `&lt;think&gt;...&lt;/think&gt;` inline reasoning parser
- **Schema Canonicalization** — Ensures valid JSON Schema for tool definitions
- **Factory Registration** — `Register()` + `protocol.New()` for extensibility
- **Content Type Detection** — 5 types (JSON/list/code/long-text/short) with per-type compression strategy
- **Live Zone** — Only compresses messages after last user message; frozen history left untouched
- **Telemetry** — `Telemetry.Record()` collects duration/vendor/model/tokens per call; `FormatLine()` / `FormatMap()` for upper layers
- **Vendor.String()** — Vendor enum now has string representation for telemetry output
- **Tool/Schema Sorting** — `toWireTools()` sorts tools by name; `sortSchemaKeys()` recursively sorts JSON keys alphabetically
- **SseFramer** (`sse_framer.go`) — Byte-level SSE frame parser + Bedrock EventStream binary framing (excluded from build via `//go:build never`; reserved for raw-TCP proxy scenarios)
- **Multi-Key Rotation** — Auto-rotate API keys on 401/403; configured via `OPENAI_API_KEYS`
- **Rate Limiter** (`ratelimit.go`) — Token bucket limiter per RPM/TPM; blocks before request
- **Context Threshold** — `Telemetry` compares prompt tokens against `ModelContextWindow`; emits `ChunkWarn` / `Response.Warning` at configurable % thresholds (warn/compress)
- **Configurable Timeout** — `StreamTimeout` replaces hardcoded 120s idle timeout
- **sync.Pool** — `buildBody()` reuses pooled `map[string]any` to cut GC churn
- **HTTP Transport Tuning** — Shared cloned transport: MaxIdleConns=100, MaxIdleConnsPerHost=20, MaxConnsPerHost=50, IdleConnTimeout=120s
- **Circuit Breaker** — Per-vendor `CircuitBreaker` (Closed/Open/HalfOpen); `SendWithRetry` records failures; auto-reset after timeout
- **RetryNotify** — Context-injected callback fires before each retry; upper layers can surface retry status
- **StreamInterruptedError** — Typed error wraps stream failures after tokens already emitted; upper layer can trigger recovery instead of hard fail
- **Stream Auto-Recovery** — `chatModel.Stream` detects `StreamInterruptedError`, sends recovery notice, and falls back to `forwardChat` to complete the response
- **Tool Sanitization** — `sanitizeTool()` assigns fallback name/description when empty, preventing API 400 errors
- **Vision/Compress Shared Transport** — Both tools reuse `protocol.DefaultTransport` instead of creating per-call `http.Client`
- **SSRF Protection** — `ValidateBaseURL()` rejects localhost/LAN/private IPs by default; `ALLOW_PRIVATE_URLS=true` overrides; `ValidateProxyEnv()` checks proxy scheme
- **Secret Redaction** — `RedactString`/`RedactHeaders`/`RedactBody`/`RedactURL` mask API keys, tokens, passwords in logs and output
- **Model Failover** — `FailoverConfig` switches to `OPENAI_FALLBACK_MODEL` after retry exhaustion; supports both Chat and Stream
- **Raw HTTP Logging** — `RawLogProcessor` + `ChunkRaw*` events emit request/response/error JSONL to `logs/raw/` via `EventBus.TryPublish`
- **ChunkType Registry** — `RegisterChunkType()` + `ChunkType.String()` for extensible event types

---

## Quick Start

### Prerequisites

- Go 1.26+
- An OpenAI-compatible API endpoint

### Setup

```bash
# Clone
git clone <repo> MiniGoAgent
cd MiniGoAgent

# Clone eino to sibling directory (required: go.mod uses replace directive)
git clone https://github.com/cloudwego/eino.git reference/eino

# Configure
set OPENAI_API_KEY=your_key
set OPENAI_BASE_URL=https://token.sensenova.cn/v1
set OPENAI_MODEL=deepseek-v4-flash

# Run
go run .
```

Open http://localhost:8080

### Docker

```bash
docker build -t mini-agent .
docker run -p 8080:8080 \
  -e OPENAI_API_KEY=your_key \
  -e OPENAI_BASE_URL=https://token.sensenova.cn/v1 \
  -e OPENAI_MODEL=deepseek-v4-flash \
  mini-agent
```

---

## Configuration

| Env | Default | Description |
|-----|---------|-------------|
| `OPENAI_API_KEY` | — | API key |
| `OPENAI_BASE_URL` | — | API endpoint |
| `OPENAI_MODEL` | `deepseek-v4-flash` | Model name |
| `PORT` | `8080` | HTTP server port |
| `LOG_LEVEL` | `INFO` | Log level (DEBUG/INFO/WARN/ERROR) |
| `LOG_DIR` | `logs` | Log directory |
| `CACHE_TTL` | `30` | Tool result cache TTL (seconds) |
| `OPENAI_API_KEYS` | — | Multi-key rotation (comma-separated) |
| `STREAM_TIMEOUT` | `120s` | Stream idle timeout |
| `RATE_LIMIT_RPM` | `0` | Max requests per minute (0=off) |
| `RATE_LIMIT_TPM` | `0` | Max tokens per minute (0=off) |
| `CONTEXT_WARN_PCT` | `40` | Context warning threshold (%) |
| `CONTEXT_COMPRESS_PCT` | `50` | Context compression signal (%) |
| `AGENT_MAX_STEP` | `12` | Max ReAct agent loop steps |
| `MAX_RECONNECT_ATTEMPTS` | `3` | SSE stream reconnect attempts |

---

## Project Structure

```
.
├── main.go                  # HTTP server, agent assembly, history persistence
├── protocol/                # LLM communication protocol layer
│   ├── types.go             # Message, ToolCall, Chunk, Protocol interface
│   ├── openai.go            # OpenAI/DeepSeek/Zhipu provider, SSE reader
│   ├── host.go              # Vendor detection (baseURL → vendor)
│   ├── retry.go             # HTTP retry with exponential backoff
│   ├── think.go             # <think> tag inline reasoning parser
│   ├── normalize.go         # Tool call pairing repair
│   ├── schema_canonicalize.go # JSON Schema normalization
│   ├── telemetry.go         # Stats collector: Record/FormatLine/FormatMap + ModelContextWindow
│   ├── content_detector.go  # Content type detection (5 types)
│   ├── content_compress.go  # Per-type content compression strategy
│   ├── sse_framer.go        # Byte-level SSE + Bedrock EventStream framing (declared, not wired)
│   ├── ratelimit.go         # Token bucket rate limiter (RPM/TPM)
│   ├── protocol_test.go           # Unit + mock tests (22 tests)
│   └── openai_integration_test.go # Integration tests (9 tests)
├── main_test.go             # Helper function + history DTO tests (7 tests)
├── tools/                   # Tool implementations
│   ├── terminal.go          # Shell command execution
│   ├── websearch.go         # Web search
│   ├── compress.go          # Text compression
│   ├── fileops.go           # Read/Write/Edit/Glob/Grep
│   ├── fileops_test.go      # File operation tests (3 tests)
│   ├── vision.go            # Image analysis
│   ├── cache.go             # Terminal command result cache
│   ├── cache_test.go        # Cache logic tests (1 test)
│   ├── filter/              # Output filter framework
│   ├── log/                 # Logging package
│   └── sessionlog/          # Per-session .md logging
├── frontend/
│   └── index.html           # Web UI
└── reference/               # Reference codebases (RTK, DeepSeek-Reasonix)
```

---

## Testing

```bash
# Unit tests (no network)
go test ./... -v

# Integration tests (requires API key)
go test -tags=integration -run "TestOpenAIChat" ./protocol/ -v
```

---

## Architecture

```
Browser (SSE)
    │
    ▼
HTTP Server (main.go)
    │
    ▼
react.Agent (eino)
    │
    ├── Tool Execution
    │   ├── Terminal (filtered, cached)
    │   ├── Web Search
    │   ├── File Operations
    │   ├── Compress
    │   └── Vision
    │
    └── chatModel (adapter · telemetry delegation)
            │
            ▼
        protocol.Protocol
            │
            ├── protocol.OpenAI (OpenAI-compatible API)
            │   ├── HTTP Retry
            │   ├── SSE Reader
            │   ├── Tool Call Accumulation
            │   ├── Think Tag Parsing
            │   ├── Content Compression (live zone)
            │   └── Telemetry.Record (duration/vendor/model/tokens)
            │
            └── protocol.NormalizeMessages
                ├── Broken JSON Repair
                ├── Orphan Tool Removal
                ├── Missing Result Backfill
                └── Name Backfill
```

---

## Development

```bash
# Build
go build -o mini-agent.exe .

# Vet
go vet ./...
```

---

# MiniGoAgent

基于 Go + [eino](https://github.com/cloudwego/eino) 框架的最小化 LLM Agent，支持 Web UI、流式聊天、终端命令、联网搜索、图片识别、多会话。

---

## 特性

- **Web UI** — SSE 流式输出，Markdown 渲染，粘贴图片
- **多会话** — `?session=<id>` 隔离对话历史，互不干扰
- **Agent 循环** — ReAct 架构（`AGENT_MAX_STEP` 可配置，默认 12 步），工具调用自动修复
- **9 个工具** — 终端、搜索、压缩、读写文件、编辑文件、Glob、Grep、图片分析
- **输出过滤** — RTK 风格，按命令前缀匹配不同过滤器，语义安全保底
- **结果缓存** — 只读终端命令 30s TTL 缓存
- **历史持久化** — Ctrl+C 自动保存，启动时恢复
- **图片分析** — 基于 StepFun API 的视觉能力
- **会话日志** — 每会话独立的 `.md` 日志文件，记录 user/assistant/tool/raw 事件
- **统计卡片** — 对话内联显示 `47s · auto · ↑92.7k · ↓1.4k · ctx 92.7k/524k 18%`
- **Raw 层日志** — 通过 context `CtxLogf` 注入，协议层关键点输出 HTTP/SSE 调试信息

### 协议层 (`protocol/`)

- **供应商检测** — 自动识别 DeepSeek/Zhipu/MiniMax/LongCat/Ollama/MiMo
- **推理内容** — 运行时自动检测 `reasoning_content`，发送 `ChunkReasoning` 事件
- **HTTP 重试** — 指数退避 + 抖动，最多 10 次，支持 `Retry-After`
- **流断线重连** — 未输出 token 前自动重连（`MAX_RECONNECT_ATTEMPTS` 可配置，默认 3 次）
- **工具调用修复** — 截断 JSON 修复、孤儿 tool 消息丢弃、缺失 result 补填、空缺名回填
- **Think 标签解析** — MiniMax `&lt;think&gt;` 内联推理解析器
- **Schema 标准化** — 确保工具定义符合 JSON Schema 规范
- **工厂注册** — `Register()` + `protocol.New()`，方便扩展
- **内容类型检测** — 5 种类型（JSON/列表/代码/长文/短文本），按类型压缩
- **Live Zone** — 只压缩最后一条 user 消息之后的内容，frozen 历史不动
- **Telemetry 统计** — `Telemetry.Record()` 记录每次调用的耗时/供应商/模型/token；`FormatLine()`/`FormatMap()` 供上层使用
- **工具/Schema 排序** — `toWireTools()` 按 name 排序工具；`sortSchemaKeys()` 递归排序 JSON key
- **SseFramer** (`sse_framer.go`) — 字节级 SSE 帧解析 + Bedrock EventStream 二进制帧（`//go:build never` 排除编译，留待 raw TCP 代理场景）
- **多 Key 轮转** — 401/403 自动换 key 重试，配置 `OPENAI_API_KEYS`
- **Rate Limiter** (`ratelimit.go`) — Token bucket 限流（RPM/TPM），请求前阻塞等待
- **上下文阈值** — `Telemetry` 对比 prompt tokens 与 model context window；超过阈值发出 `ChunkWarn` / `Response.Warning`
- **可配置超时** — `StreamTimeout` 替代硬编码 120s idle timeout
- **sync.Pool 复用** — `buildBody()` 复用 `map[string]any`，降低 GC 压力
- **HTTP 连接池调优** — 共享 Transport，MaxIdleConns=100，MaxIdleConnsPerHost=20，MaxConnsPerHost=50，KeepAlive 120s
- **断路器（Circuit Breaker）** — 按 vendor 独立状态机（Closed/Open/HalfOpen）；`SendWithRetry` 记录失败；超时后自动恢复
- **RetryNotify** — context 注入重试回调，上层可展示重试状态
- **StreamInterruptedError** — 已 emit 后断线包装为独立 error 类型，上层可识别并恢复
- **流中断自动恢复** — `chatModel.Stream` 检测到 `StreamInterruptedError` 后发恢复提示，用 `forwardChat` 补全响应
- **工具参数兜底** — `sanitizeTool()` 为空 name/description 自动赋 fallback，避免 API 400
- **Vision/Compress 共享 Transport** — 两个工具均复用 `protocol.DefaultTransport`，不再各自创建 `http.Client`

---

## 快速开始

### 前置条件

- Go 1.26+
- OpenAI 兼容 API

### 配置运行

```bash
# 克隆 eino 到同级目录（go.mod 使用了 replace 指令）
git clone https://github.com/cloudwego/eino.git reference/eino

set OPENAI_API_KEY=your_key
set OPENAI_BASE_URL=https://token.sensenova.cn/v1
set OPENAI_MODEL=deepseek-v4-flash

go run .
```

打开 http://localhost:8080

---

## 配置项

| 环境变量 | 默认值 | 说明 |
|---------|--------|------|
| `OPENAI_API_KEY` | — | API 密钥 |
| `OPENAI_BASE_URL` | — | API 地址 |
| `OPENAI_MODEL` | `deepseek-v4-flash` | 模型名称 |
| `PORT` | `8080` | HTTP 端口 |
| `LOG_LEVEL` | `INFO` | 日志级别 |
| `LOG_DIR` | `logs` | 日志目录 |
| `CACHE_TTL` | `30` | 工具结果缓存 TTL（秒） |
| `OPENAI_API_KEYS` | — | 多 key 轮转（逗号分隔） |
| `STREAM_TIMEOUT` | `120s` | 流空闲超时 |
| `RATE_LIMIT_RPM` | `0` | 每分钟请求限制（0=关闭） |
| `RATE_LIMIT_TPM` | `0` | 每分钟 token 限制（0=关闭） |
| `CONTEXT_WARN_PCT` | `40` | 上下文预警阈值（%） |
| `CONTEXT_COMPRESS_PCT` | `50` | 上下文压缩触发阈值（%） |
| `AGENT_MAX_STEP` | `12` | Agent 最大循环步数 |
| `MAX_RECONNECT_ATTEMPTS` | `3` | SSE 流重连次数 |

---

## 项目结构

```
.
├── main.go                  # HTTP 服务、Agent 组装、历史持久化
├── protocol/                # LLM 通信协议层（零 eino 依赖）
│   ├── types.go             # Message/ToolCall/Chunk/Protocol 接口
│   ├── openai.go            # OpenAI/DeepSeek/Zhipu 实现、SSE 解析
│   ├── host.go              # 供应商检测
│   ├── retry.go             # HTTP 重试（指数退避）
│   ├── think.go             # MiniMax <think> 标签解析
│   ├── normalize.go         # 工具调用配对修复
│   ├── schema_canonicalize.go # JSON Schema 标准化
│   ├── telemetry.go         # 统计收集：Record/FormatLine/FormatMap + ModelContextWindow
│   ├── content_detector.go  # 内容类型检测（5 种）
│   ├── content_compress.go  # 按类型压缩策略
│   ├── sse_framer.go        # SSE + Bedrock EventStream 帧解析（声明未接入）
│   ├── ratelimit.go         # Token bucket 限流器（RPM/TPM）
│   ├── protocol_test.go           # 单元 + Mock 测试（25 个）
│   └── openai_integration_test.go # 集成测试（9 个）
├── main_test.go             # 辅助函数 + 历史 DTO 测试（7 个）
├── tools/                   # 工具实现
│   ├── terminal.go          # Shell 命令执行
│   ├── websearch.go         # 联网搜索
│   ├── compress.go          # 文本压缩
│   ├── fileops.go           # 读写编辑文件/Glob/Grep
│   ├── fileops_test.go      # 文件操作测试（3 个）
│   ├── vision.go            # 图片分析
│   ├── cache.go             # 终端命令缓存
│   ├── cache_test.go        # 缓存逻辑测试（1 个）
│   ├── filter/              # 输出过滤器
│   ├── log/                 # 日志包
│   └── sessionlog/          # 每会话 .md 日志
├── frontend/
│   └── index.html           # Web 界面
└── reference/               # 参考代码
```

---

## 测试

```bash
# 单元测试（无需网络）
go test ./... -v

# 集成测试（需 API key）
go test -tags=integration -run "TestOpenAIChat" ./protocol/ -v
```

---

## 架构

```
浏览器 (SSE)
    │
    ▼
HTTP 服务 (main.go)
    │
    ▼
react.Agent (eino)
    │
    ├── 工具执行
    │   ├── 终端 (过滤 + 缓存)
    │   ├── 联网搜索
    │   ├── 文件操作
    │   ├── 文本压缩
    │   └── 图片分析
    │
    └── chatModel (适配层)
            │
            ▼
        protocol.Protocol
            │
            └── protocol.OpenAI
                ├── HTTP 重试
                ├── SSE 读取
                ├── 工具调用累积
                └── Think 标签解析
```
