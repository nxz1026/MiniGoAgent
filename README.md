# MiniGoAgent

Minimal LLM Agent powered by Go + [eino](https://github.com/cloudwego/eino) — Web UI, streaming chat, terminal, web search, vision, multi-session.

---

## Features

- Web UI with SSE streaming, markdown, image paste
- Multi-session: `?session=<id>` isolates conversations
- ReAct agent loop (up to 12 steps), auto tool-call repair
- 8 tools: terminal, web search, text compression, read/write/edit/glob/grep files, image analysis
- Command-aware output filter, 30s TTL result cache
- Per-session `.md` logs, auto-save on Ctrl+C
- Telemetry card inline in chat: `47s · auto · ↑92.7k · ↓1.4k · ctx 92.7k/524k 18%`
- Multi-provider: OpenAI, DeepSeek, Zhipu, MiniMax, LongCat, Ollama Cloud, MiMo

---

## Quick Start

```bash
set OPENAI_API_KEY=your_key
set OPENAI_BASE_URL=https://api.example.com/v1
set OPENAI_MODEL=deepseek-v4-flash

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
| `PORT` | `8080` | Server port |
| `LOG_LEVEL` | `INFO` | Log level |
| `LOG_DIR` | `logs` | Log directory |
| `CACHE_TTL` | `30` | Tool result cache TTL (seconds) |

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
- ReAct Agent 循环（最多 12 步），自动修复工具调用
- 8 个工具：终端、搜索、压缩、读写编辑文件、Glob、Grep、图片分析
- 输出过滤 + 30s 缓存，命令感知
- 每会话独立 .md 日志，Ctrl+C 自动保存
- 统计卡片内联显示：`47s · auto · ↑92.7k · ↓1.4k · ctx 92.7k/524k 18%`
- 多供应商：OpenAI、DeepSeek、Zhipu、MiniMax、LongCat、Ollama Cloud、MiMo

---

## 快速开始

```bash
set OPENAI_API_KEY=your_key
set OPENAI_BASE_URL=https://api.example.com/v1
set OPENAI_MODEL=deepseek-v4-flash

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
| `PORT` | `8080` | 服务端口 |
| `LOG_LEVEL` | `INFO` | 日志级别 |
| `LOG_DIR` | `logs` | 日志目录 |
| `CACHE_TTL` | `30` | 工具缓存 TTL（秒） |

---

## 技术细节

详见 [README.detail.md](README.detail.md)：架构设计、协议层详解、项目结构、测试方法。
