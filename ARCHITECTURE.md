# MiniGoAgent Architecture Freeze

> Date: 2026-07-10
> Purpose: freeze the current real architecture before larger ADK refactoring.
> Rule: this document describes the actual codebase first. Future target layers are noted only as boundaries, not as completed implementation.

---

## 1. Current Architecture Principles

MiniGoAgent is a Go + eino based minimal agent with a deliberately independent LLM protocol layer. The current stable direction is:

- Keep RAW/provider behavior reliable before expanding ADK features.
- Keep `protocol/` free of eino types.
- Keep tool implementations isolated under `tools/` until the ADK/tool lifecycle design is settled.
- Keep prompt optimization outside the online request path.
- Prefer small boundary-building refactors over large import-path migrations.

---

## 2. Current Real Layers

```text
Browser / frontend/index.html
    |
    v
main.go
    - HTTP handlers
    - SSE streaming
    - session history persistence
    - chatModel adapter from eino to protocol
    - app bootstrap and dependency assembly
    |
    v
eino react.Agent
    |
    +--> tools/                   # terminal/search/compress/file/vision skills
    |
    +--> chatModel                 # adapter boundary
            |
            v
        protocol.Protocol          # LLM provider protocol interface
            |
            v
        protocol.OpenAI            # OpenAI-compatible provider implementation
```

Current package responsibilities:

| Package/File | Current Responsibility | Boundary Notes |
|---|---|---|
| `main.go` | App bootstrap, dependency assembly | Thin wiring layer; reads config, builds proto/agent/server, starts HTTP |
| `internal/config` | Environment loading and typed application configuration | First extracted internal layer; main consumes `config.Load()` |
| `internal/session` | In-memory session state, snapshot/append, JSON history persistence | Manager wraps session map + lock; uses `Marshal`/`Unmarshal` DTO conversion |
| `internal/server` | HTTP handlers, SSE streaming, ChatModel adapter, request helpers, vision tools | Exports `Server`, `ChatModel`, `AgentRunner`, `PromptProvider` |
| `protocol/` | LLM protocol, OpenAI-compatible wire format, stream parsing, retry/failover/telemetry/raw logging/usage analytics | This is not MCP; it is closer to LLM provider protocol/RAW gateway |
| `tools/` | Agent-callable skill implementations | Keep stable until ADK tool lifecycle is designed |
| `tools/filter/` | RTK-style terminal output filtering | Already wired through `tools/terminal.go` |
| `tools/sessionlog/` | Per-session markdown logs | Used by HTTP/session layer |
| `frontend/` | Single-file browser UI | Calls `/api/chat`, `/api/chat/stream`, `/api/vision/native` |
| `reference/eino` | Local eino dependency through go.mod replace | Intentional for now, but git state must stay explicit |

---

## 3. Runtime Data Flow

### Non-stream chat

```text
HTTP POST /api/chat
  -> chatServer.handleChat
  -> chatModel.Generate
  -> protocol.OpenAI.Chat
  -> SendWithRetry / failover / raw logging
  -> Telemetry.RecordFromContext
  -> optional UsageTracker SQLite write
  -> response returned to frontend
```

### Stream chat

```text
HTTP POST /api/chat/stream
  -> chatServer.handleChatStream
  -> chatModel.Stream
  -> protocol.OpenAI.Stream
  -> streamWithFailover
  -> readStream scanner parses SSE data lines
  -> Chunk events emitted to chatModel
  -> EventBus publishes telemetry/log/raw processors
  -> SSE response to frontend
```

### Tool execution

```text
eino ToolNode
  -> tools.RunTerminal / WebSearch / RunCompress / file ops / RunVision
  -> terminal command interceptors
  -> command execution
  -> RTK-style filter.Run output filtering
  -> optional cache for read-only commands
```

---

## 4. RAW / Protocol Layer Boundary

`protocol/` currently owns the LLM provider protocol boundary. It should remain responsible for:

- OpenAI-compatible request/response conversion.
- SSE stream parsing and chunk emission.
- Provider/vendor detection and policy differences.
- Retry, reconnect, rate limit, circuit breaker, health check, failover.
- RAW event emission and raw HTTP logging.
- Secret redaction and SSRF defense for provider URLs.
- Usage telemetry and local query API.
- Lifecycle hooks around provider calls.

`protocol/` should not own:

- Agent reasoning loop policy.
- Prompt self-optimization.
- Session memory strategy beyond context metadata.
- Tool permission and orchestration policy.
- Frontend/UI event model.

Do not rename or delete `protocol/` until `main.go` is smaller and the ADK boundaries are explicitly designed.

---

## 5. RAW Layer Completed Optimizations

These are already implemented and must be preserved during future refactors.

### 5.1 RTK-Style Terminal Output Filtering

- Location: `tools/filter/`
- Entry point: `tools/terminal.go` calls `filter.Run(result.Output, cmdCtx.Command)`.
- Behavior: command-aware filtering for terminal output, including `ls`, `git`, and general fallback filters.
- Purpose: reduce noisy terminal output before it returns to the agent.

### 5.2 Headroom-Inspired Content Compression

- Locations: `protocol/content_detector.go`, `protocol/content_predict.go`, `protocol/content_compress.go`.
- Entry point: `protocol.OpenAI.toWireMessages()` calls `DetectContentType()` and `CachedCompressContent()` for live-zone user/tool content over threshold.
- Implemented strategies: JSON array minification, build output cleanup, search result compaction, git diff hunk limiting, safe fallback for other text.
- Cache: SHA-256 keyed compression cache with TTL and max size.
- Boundary: this is implemented as local Go logic inspired by Headroom-style context reduction; there is no runtime dependency on a separate Headroom service/package.

### 5.3 OpenAI-Compatible Protocol Support

- Location: `protocol/openai.go`.
- Interface: `protocol.Protocol` with `Chat(ctx, req)` and `Stream(ctx, req)`.
- Supports: OpenAI-compatible chat completions, streaming SSE, tool calls, reasoning content, usage parsing, multimodal message content, model/vendor policy fields.
- Providers covered by detection/policy: OpenAI-compatible default, DeepSeek, Zhipu, MiniMax, LongCat, MiMo, Ollama Cloud, StepFun, Qwen.

### 5.4 Reliability and Safety Optimizations

- HTTP retry with exponential backoff, jitter, and `Retry-After`: `protocol/retry.go`.
- Stream reconnect before token emission: `protocol/openai.go`.
- Typed stream interruption after token emission: `StreamInterruptedError` in `protocol/types.go`.
- Per-vendor circuit breaker: `CircuitBreaker` and `vendorCircuitBreaker`.
- Health checking: `HealthChecker` / `HealthManager`.
- RPM/TPM token bucket rate limiting: `protocol/ratelimit.go`.
- Multi-key rotation on auth failures: `OpenAI.rotateKey()`.
- SSRF defense for provider base URLs: `ValidateBaseURL()`.
- Proxy scheme validation: `ValidateProxyEnv()`.
- Secret redaction in strings, URLs, headers, and bodies: `protocol/redact.go`.

### 5.5 Observability and Query Optimizations

- EventBus pub/sub for chunks: `protocol/stream.go`.
- Raw HTTP JSONL logs through `RawLogProcessor`: `protocol/raw_log.go`.
- `ChunkRawRequest`, `ChunkRawResponse`, `ChunkRawError` typed events.
- Telemetry at protocol layer: duration, model, vendor, prompt/completion tokens.
- Session-aware usage persistence: `protocol/usage_tracker.go`, SQLite at `logs/raw/usage.db` when `USAGE_DB=1`.
- Structured RAW query API: `protocol/raw_query.go` for daily/model/vendor stats.
- Local-only MCP WebSocket query endpoint: `protocol/mcp_server.go`, mounted at `/mcp` when `USAGE_DB=1`.
- Lifecycle hooks: `BeforeProcess`, `AfterProcess`, `OnError` in `protocol/hooks.go`.

### 5.6 Efficiency Optimizations

- Request body map reuse with `sync.Pool`: `bodyPool` in `protocol/openai.go`.
- Shared HTTP transport with tuned connection pool: `DefaultTransport` and `NewHTTPClient()`.
- Vision and compression tools reuse protocol HTTP transport.
- Tool/schema sorting and schema key canonicalization reduce diff/cache noise.
- Tool sanitization fills empty names/descriptions before provider calls.

---

## 6. ADK / Agent Runtime Current State

Current runtime uses eino `react.Agent` directly from `main.go` through `chatModel`.

Current responsibilities still in `main.go`:

- Build protocol provider from `internal/config.Config`.
- Build ChatModel adapter via `server.NewChatModel`.
- Infer and register tools.
- Configure `react.Agent`.

Future ADK boundary should introduce:

- `AgentRunner`: stable interface for chat/stream execution.
- `PromptProvider`: stable interface for retrieving active prompt versions.
- `SessionStore`: stable interface for session history snapshots and appends.
- `ToolExecutor` or `ToolRegistry`: stable interface for tool discovery and execution policy.

Do not rewrite the ReAct loop until these interfaces are agreed.

---

## 7. Tools / Skill Layer Current State

`tools/` is the current Skill layer. It contains concrete tool implementations:

- `terminal.go`: Windows command execution, interceptor chain, RTK-style filtering.
- `websearch.go`: Firecrawl search wrapper.
- `compress.go`: LLM-backed text compression tool.
- `fileops.go`: read/write/edit/glob/grep file operations.
- `vision.go`: image analysis through configured vision provider.
- `cache.go`: TTL cache for read-only terminal commands.
- `interceptor.go`: command interceptor registry.

Future migration to `internal/skill/` is allowed only after ADK tool lifecycle and permission policy are designed.

---

## 8. MCP / Usage Query Current State

Current MCP server is not the tool protocol layer. It is a local WebSocket query endpoint for usage analytics.

- Location: `protocol/mcp_server.go`.
- Enabled by: `USAGE_DB=1`.
- Endpoint: `ws://localhost:<PORT>/mcp`.
- Access: local-only remote address and loopback Origin.
- Methods: `get_stats`, `get_daily`, `get_models`, `get_vendors`, `query_records`.

If a future standard MCP tool server is added, it should be a separate package and should not be confused with this usage-query endpoint.

---

## 9. Prompt Optimization / DSPy Loop Boundary

Prompt self-iteration belongs beside the ADK layer as an offline or semi-offline optimization sidecar. It must not be placed in RAW, Gateway, MCP, or Skill packages.

Recommended future package boundary:

```text
internal/promptlab/
    dspy_loop/              # Python DSPy scripts or service client
    evalset/                # build evaluation samples from logs and curated cases
    promptstore/            # prompt versions, scores, rollback metadata
    promotion/              # promotion policy from candidate to stable prompt
```

Runtime relationship:

```text
ADK runtime -> PromptProvider -> stable prompt version
PromptLab/DSPy -> candidate prompt versions -> eval -> promotion -> stable prompt
```

Rules:

- Online requests must read only the stable prompt version.
- DSPy optimization must not run inline during user requests.
- Candidate prompts must be evaluated against fixed test/eval sets before promotion.
- Prompt versions must be auditable and rollbackable.
- Session logs, tool traces, raw usage stats, and curated failures may feed the eval dataset builder.

First implementation should prefer offline Python scripts over a mandatory always-on service.

---

## 10. Known Technical Debt

- `main.go` is now a thin wiring layer; server, session, and adapter extracted. `internal/server` is the only package that knows about eino types and HTTP simultaneously.
- `protocol/` name is broad. It currently means LLM provider protocol/RAW gateway, not MCP.
- `reference/eino` is a local replace dependency and currently appears as an abnormal/untracked git status item in this workspace.
- README and MEMO are append-heavy and can contain old counts in earlier sections; latest sections are authoritative.
- Some integration behavior depends on external provider compatibility and is not covered by network-free tests.

---

## 11. Next Refactor Guardrails

Completed steps:

- `internal/session` — session state, snapshot/append, JSON history persistence extracted from `main.go`.
- `internal/server` — HTTP handlers, SSE streaming, ChatModel adapter, vision helpers extracted from `main.go`.
- `AgentRunner` and `PromptProvider` interfaces defined in `internal/server`.

Allowed next steps:

- Replace `*react.Agent` usage with `AgentRunner` interface in `Server`.
- Wire `PromptProvider` into `Server` for configurable system prompts.

Do not do yet:

- Do not delete or rename `protocol/`.
- Do not move `tools/` to `internal/skill/`.
- Do not remove `reference/eino` replace.
- Do not run DSPy optimization in the online request path.
- Do not mix prompt optimization with RAW provider calling logic.
