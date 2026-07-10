# MiniGoAgent

Minimal LLM Agent powered by Go + [eino](https://github.com/cloudwego/eino) — Web UI, streaming chat, terminal, web search, vision, multi-session.

---

## Features

- Web UI with SSE streaming, markdown, image paste
- Multi-session: `?session=<id>` isolates conversations
- ReAct agent loop (configurable max steps, default 12), auto tool-call repair
- 9 tools: terminal, web search, text compression, read/write/edit/glob/grep files, image analysis
- Command-aware output filter, 30s TTL result cache
- Per-session `.md` logs, auto-save on Ctrl+C
- Telemetry card inline in chat: `47s · auto · ↑92.7k · ↓1.4k · ctx 92.7k/524k 18%`
- Multi-provider: OpenAI, DeepSeek, Zhipu, MiniMax, LongCat, Ollama Cloud, MiMo, StepFun, Qwen
- Vision: Native Mode (multimodal) for GPT-4o/DeepSeek-VL/Qwen-VL/Step-2, Tool Mode fallback for other models
- Stream interruption auto-recovery: transparent fallback on mid-stream disconnect
- Per-host connection pool: MaxConnsPerHost=50 with shared HTTP transport
- Circuit breaker: per-vendor failure isolation and auto-recovery
- SSRF protection: blocks localhost/LAN/private IPs by default (ALLOW_PRIVATE_URLS=true to override)
- Secret redaction: logs and raw output redact API keys, tokens, passwords
- Workspace sandbox: file tools (read/write/edit/glob/grep) restricted to WORKSPACE_ROOT (default `.`)
- Image SSRF protection: fetchImage rejects private/internal URLs unless ALLOW_PRIVATE_IMAGE_URLS=true
- Model failover: primary model falls back to OPENAI_FALLBACK_MODEL on persistent errors
- Raw HTTP logging: enable with RAW_LOG=1, writes to logs/raw/ (JSONL)
- Usage analytics: enable with USAGE_DB=1, writes to logs/raw/usage.db
- Local-only MCP WebSocket: enabled with USAGE_DB=1 at ws://localhost:PORT/mcp
- Layered architecture: `internal/adk/` agent runtime (tool registry, middleware, events, session) between HTTP handlers and protocol layer

---

## Quick Start

```bash
# 1. Clone eino to sibling directory (required: go.mod uses replace directive)
git clone https://github.com/cloudwego/eino.git reference/eino

# 2. Configure
set OPENAI_API_KEY=your_key
set OPENAI_BASE_URL=https://api.example.com/v1
set OPENAI_MODEL=deepseek-v4-flash

# 3. Run
go run .
# Open http://localhost:8080
```

### Docker

```bash
docker build -t mini-agent .
docker run -p 8080:8080 \
  -e OPENAI_API_KEY=your_key \
  -e OPENAI_BASE_URL=https://api.example.com/v1 \
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
| `OPENAI_FALLBACK_MODEL` | — | Fallback model on persistent errors |
| `OPENAI_FALLBACK_BASE_URL` | — | Fallback API endpoint |
| `PORT` | `8080` | Server port |
| `LOG_LEVEL` | `INFO` | Log level |
| `LOG_DIR` | `logs` | Log directory |
| `CACHE_TTL` | `30` | Tool result cache TTL (seconds) |
| `OPENAI_API_KEYS` | — | Multi-key rotation (comma-separated) |
| `STREAM_TIMEOUT` | `120s` | Stream idle timeout |
| `RATE_LIMIT_RPM` | `0` | Max requests per minute (0=off) |
| `RATE_LIMIT_TPM` | `0` | Max tokens per minute (0=off) |
| `CONTEXT_WARN_PCT` | `40` | Context warning threshold (%) |
| `CONTEXT_COMPRESS_PCT` | `50` | Context compression signal (%) |
| `AGENT_MAX_STEP` | `12` | Max ReAct agent loop steps |
| `ALLOW_PRIVATE_URLS` | `false` | Allow localhost/LAN URLs (security risk) |
| `ALLOW_PRIVATE_IMAGE_URLS` | `false` | Allow private/internal image URLs in vision tool (security risk) |
| `WORKSPACE_ROOT` | `.` | Workspace root for file tools (ReadFile/WriteFile/EditFile/GlobFiles/GrepFiles) |
| `RAW_LOG` | `0` | Enable raw HTTP logging (1=on) |
| `RAW_LOG_DIR` | `logs/raw` | Raw log directory |
| `USAGE_DB` | `0` | Enable SQLite usage analytics and local-only MCP endpoint |
| `MAX_RECONNECT_ATTEMPTS` | `3` | SSE stream reconnect attempts |

---

## Technical Details

See [README.detail.md](README.detail.md) for architecture, protocol layer, project structure, and testing.

---

# MiniGoAgent

基于 Go + [eino](https://github.com/cloudwego/eino) 的最小化 LLM Agent — Web UI、流式聊天、终端、联网搜索、图像识别、多会话。

---

## 功能特性

- Web UI：SSE 流式输出、Markdown 渲染、粘贴图片
- 多会话：`?session=<id>` 隔离对话历史
- ReAct Agent 循环（最大步数可配置，默认 12），自动修复工具调用
- 9 个工具：终端、搜索、压缩、读写编辑文件、Glob、Grep、图片分析
- 输出过滤 + 30s 缓存，命令感知
- 每会话独立 .md 日志，Ctrl+C 自动保存
- 统计卡片内联显示：`47s · auto · ↑92.7k · ↓1.4k · ctx 92.7k/524k 18%`
- 多供应商：OpenAI、DeepSeek、Zhipu、MiniMax、LongCat、Ollama Cloud、MiMo、StepFun、Qwen
- 图像识别：Native Mode（GPT-4o/DeepSeek-VL/Qwen-VL/Step-2 直通多模态），Tool Mode 降级
- 流中断自动恢复：中途断线后静默重连补全，不丢对话上下文
- Per-Host 连接池：共享 Transport，MaxConnsPerHost=50
- 断路器：按供应商故障隔离 + 自动恢复
- SSRF 防护：默认阻止 localhost/LAN/私有 IP（ALLOW_PRIVATE_URLS=true 关闭）
- Secret 脱敏：日志和原始输出自动脱敏 API key/token/password
- 工作区沙箱：文件工具（读写编辑/Glob/Grep）限制在 WORKSPACE_ROOT 内（默认 `.`）
- 图片 SSRF 防护：fetchImage 拒绝内网/私有 URL（ALLOW_PRIVATE_IMAGE_URLS=true 放行）
- 模型级 Failover：主模型持续失败时切换到 OPENAI_FALLBACK_MODEL
- Raw HTTP 日志：RAW_LOG=1 启用，写入 logs/raw/（JSONL）
- Usage 分析：USAGE_DB=1 启用，写入 logs/raw/usage.db
- 本机限定 MCP WebSocket：USAGE_DB=1 后启用 ws://localhost:PORT/mcp
- 分层架构：`internal/adk/` Agent 运行时（工具注册表、中间件、事件总线、会话持久化）隔离 HTTP handler 和协议层

---

## 快速开始

```bash
# 1. 克隆 eino 到 reference 目录（go.mod 使用了 replace 指令）
git clone https://github.com/cloudwego/eino.git reference/eino

# 2. 配置环境变量
set OPENAI_API_KEY=your_key
set OPENAI_BASE_URL=https://api.example.com/v1
set OPENAI_MODEL=deepseek-v4-flash

# 3. 运行
go run .
# 打开 http://localhost:8080
```

### Docker

```bash
docker build -t mini-agent .
docker run -p 8080:8080 \
  -e OPENAI_API_KEY=your_key \
  -e OPENAI_BASE_URL=https://api.example.com/v1 \
  -e OPENAI_MODEL=deepseek-v4-flash \
  mini-agent
```

---

## 配置项

| 环境变量 | 默认值 | 说明 |
|---------|--------|------|
| `OPENAI_API_KEY` | — | API 密钥 |
| `OPENAI_BASE_URL` | — | API 地址 |
| `OPENAI_MODEL` | `deepseek-v4-flash` | 模型名称 |
| `OPENAI_FALLBACK_MODEL` | — | 故障转移模型 |
| `OPENAI_FALLBACK_BASE_URL` | — | 故障转移 API 地址 |
| `ALLOW_PRIVATE_URLS` | `false` | 允许 localhost/LAN URL（安全风险） |
| `ALLOW_PRIVATE_IMAGE_URLS` | `false` | 允许 vision 工具访问私有/内网图片 URL（安全风险） |
| `WORKSPACE_ROOT` | `.` | 文件工具工作区根目录（ReadFile/WriteFile/EditFile/GlobFiles/GrepFiles） |
| `RAW_LOG` | `0` | 启用原始 HTTP 日志（1=开启） |
| `RAW_LOG_DIR` | `logs/raw` | 原始日志目录 |
| `USAGE_DB` | `0` | 启用 SQLite 用量分析和本机限定 MCP 端点 |
| `PORT` | `8080` | 服务端口 |
| `LOG_LEVEL` | `INFO` | 日志级别 |
| `LOG_DIR` | `logs` | 日志目录 |
| `CACHE_TTL` | `30` | 工具缓存 TTL（秒） |
| `OPENAI_API_KEYS` | — | 多 key 轮转（逗号分隔） |
| `STREAM_TIMEOUT` | `120s` | 流空闲超时 |
| `RATE_LIMIT_RPM` | `0` | 每分钟请求限制（0=关闭） |
| `RATE_LIMIT_TPM` | `0` | 每分钟 token 限制（0=关闭） |
| `CONTEXT_WARN_PCT` | `40` | 上下文预警阈值（%） |
| `CONTEXT_COMPRESS_PCT` | `50` | 上下文压缩触发阈值（%） |
| `AGENT_MAX_STEP` | `12` | Agent 最大循环步数 |
| `MAX_RECONNECT_ATTEMPTS` | `3` | SSE 流重连次数 |

---

## 技术细节

详见 [README.detail.md](README.detail.md)：架构设计、协议层详解、项目结构、测试方法。
