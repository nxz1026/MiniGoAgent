# MiniGoAgent

A minimal LLM Agent built with Go and the [eino](https://github.com/cloudwego/eino) framework, featuring Web UI, streaming chat, terminal execution, web search, image recognition, and multi-session conversations.

---

## Features

- **Web UI** — Chat interface with streaming SSE output, markdown rendering, image paste
- **Multi-session** — `?session=<id>` isolates conversation histories
- **Agent Loop** — ReAct agent with up to 12 steps, tool-call pairing repair, message rewriting
- **8 Tools** — Terminal, web search, text compression, file read/write/edit, glob, grep
- **Output Filter** — RTK-style, command-aware filtering (ls/git/general), semantic-safe fallback
- **Result Cache** — 30s TTL for read-only terminal commands
- **History Persistence** — Auto-save on Ctrl+C, restore on startup
- **Image Analysis** — Vision via StepFun API
- **Logging** — 4-level (DEBUG/INFO/WARN/ERROR), stdout + daily rotation

### Protocol Layer (`protocol/`)

- **Vendor Detection** — Auto-detects DeepSeek, Zhipu, MiniMax, LongCat, Ollama Cloud, MiMo from baseURL
- **Reasoning Content** — Runtime detection; emits `ChunkReasoning` events on any provider that returns `reasoning_content`
- **HTTP Retry** — Exponential backoff + jitter, 10 max retries, `Retry-After` support, auth retry
- **Stream Reconnect** — 3 automatic reconnection attempts before any token emitted
- **Tool Call Normalization** — Broken JSON repair, orphan tool message removal, missing result backfill, name backfill
- **Think Tag** — MiniMax `&lt;think&gt;...&lt;/think&gt;` inline reasoning parser
- **Schema Canonicalization** — Ensures valid JSON Schema for tool definitions
- **Factory Registration** — `Register()` + `protocol.New()` for extensibility

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

# Install eino dependency (vendored)
replace github.com/cloudwego/eino => ../eino

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
│   ├── protocol_test.go     # Unit + mock tests (22 tests)
│   └── openai_integration_test.go # Integration tests (9 tests)
├── tools/                   # Tool implementations
│   ├── terminal.go          # Shell command execution
│   ├── websearch.go         # Web search
│   ├── compress.go          # Text compression
│   ├── fileops.go           # Read/Write/Edit/Glob/Grep
│   ├── vision.go            # Image analysis
│   ├── cache.go             # Terminal command result cache
│   ├── filter/              # Output filter framework
│   └── log/                 # Logging package
├── frontend/
│   └── index.html           # Web UI
└── reference/               # Reference codebases (RTK, DeepSeek-Reasonix)
```

---

## Testing

```bash
# Unit tests (no network, 22 tests)
go test ./protocol/ -v

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
    │   └── Compress
    │
    └── chatModel (adapter)
            │
            ▼
        protocol.Protocol
            │
            ├── protocol.OpenAI (OpenAI-compatible API)
            │   ├── HTTP Retry
            │   ├── SSE Reader
            │   ├── Tool Call Accumulation
            │   └── Think Tag Parsing
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
- **Agent 循环** — ReAct 架构，最多 12 步，工具调用自动修复
- **8 个工具** — 终端、搜索、压缩、读写文件、编辑文件、Glob、Grep
- **输出过滤** — RTK 风格，按命令前缀匹配不同过滤器，语义安全保底
- **结果缓存** — 只读终端命令 30s TTL 缓存
- **历史持久化** — Ctrl+C 自动保存，启动时恢复
- **图片分析** — 基于 StepFun API 的视觉能力
- **日志系统** — 4 级日志，stdout + 按天文件轮转

### 协议层 (`protocol/`)

- **供应商检测** — 自动识别 DeepSeek/Zhipu/MiniMax/LongCat/Ollama/MiMo
- **推理内容** — 运行时自动检测 `reasoning_content`，发送 `ChunkReasoning` 事件
- **HTTP 重试** — 指数退避 + 抖动，最多 10 次，支持 `Retry-After`
- **流断线重连** — 未输出 token 前自动重连 3 次
- **工具调用修复** — 截断 JSON 修复、孤儿 tool 消息丢弃、缺失 result 补填、空缺名回填
- **Think 标签解析** — MiniMax `&lt;think&gt;` 内联推理解析器
- **Schema 标准化** — 确保工具定义符合 JSON Schema 规范
- **工厂注册** — `Register()` + `protocol.New()`，方便扩展

---

## 快速开始

### 前置条件

- Go 1.26+
- OpenAI 兼容 API

### 配置运行

```bash
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
│   ├── protocol_test.go     # 单元 + Mock 测试（22 个）
│   └── openai_integration_test.go # 集成测试（9 个）
├── tools/                   # 工具实现
├── frontend/
│   └── index.html           # Web 界面
└── reference/               # 参考代码
```

---

## 测试

```bash
# 单元测试（无需网络）
go test ./protocol/ -v

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
    │   └── 文本压缩
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
