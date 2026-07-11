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
- Vendor policy centralized in `protocol/vendor_policy.go`: `buildThinkingFields`, `resolveReasoningPolicy`, `defaultHealthEndpoint`. Extracted from repeated switch blocks in `openai.go`.

### 5.4 Reliability and Safety Optimizations

- HTTP retry with exponential backoff, jitter, and `Retry-After`: `protocol/retry.go`.
- Stream reconnect before token emission: `protocol/openai.go`.
- Typed stream interruption after token emission: `StreamInterruptedError` in `protocol/types.go`.
- Circuit breaker concurrency safety: `CircuitBreaker.Check()` uses exclusive `Lock()` for state writes, eliminating data race under `RLock()`.
- Per-vendor circuit breaker: `CircuitBreaker` and `vendorCircuitBreaker`.
- Health checking: `HealthChecker` / `HealthManager`.
- RPM/TPM token bucket rate limiting: `protocol/ratelimit.go`.
- Multi-key rotation on auth failures: `OpenAI.rotateKey()`.
- SSRF defense for provider base URLs: `ValidateBaseURL()`.
- Proxy scheme validation: `ValidateProxyEnv()`.
- Secret redaction in strings, URLs, headers, and bodies: `protocol/redact.go`.
- Workspace root path validation: `tools/fileops.go` `ValidatePath` enforces `WORKSPACE_ROOT` boundary for ReadFile/WriteFile/EditFile/GlobFiles/GrepFiles; `internal/server/server.go` `extractLocalImagePath` also validated.
- SSRF defense for image fetch URLs: `tools/vision.go` `fetchImage` rejects private/internal hosts unless `ALLOW_PRIVATE_IMAGE_URLS=true`.
- Secure temp file handling: `tools/terminal.go` `runElevated` uses `os.CreateTemp` with random suffix and deferred cleanup.

### 5.5 Observability and Query Optimizations

- EventBus pub/sub for chunks: `protocol/stream.go`.
- Raw HTTP JSONL logs through `RawLogProcessor`: `protocol/raw_log.go`.
- `ChunkRawRequest`, `ChunkRawResponse`, `ChunkRawError` typed events.
- Telemetry at protocol layer: duration, model, vendor, prompt/completion tokens.
- Session-aware usage persistence: `protocol/usage_tracker.go`, SQLite at `logs/raw/usage.db` when `USAGE_DB=1`.
- Structured RAW query API: `protocol/raw_query.go` for daily/model/vendor stats.
- Local-only MCP WebSocket query endpoint: `protocol/mcp_server.go`, mounted at `/mcp` when `USAGE_DB=1`.
- Lifecycle hooks: `BeforeProcess`, `AfterProcess`, `OnError` in `protocol/hooks.go`.
- `RecordFromContext` simplifies session-aware telemetry recording with direct context extraction.

### 5.6 Efficiency Optimizations

- Request body map reuse with `sync.Pool`: `bodyPool` in `protocol/openai.go`.
- Shared HTTP transport with tuned connection pool: `DefaultTransport` and `NewHTTPClient()`.
- Vision and compression tools reuse protocol HTTP transport.
- Tool/schema sorting and schema key canonicalization reduce diff/cache noise.
- Tool sanitization fills empty names/descriptions before provider calls.
- Secure elevated command temp files: `tools/terminal.go` uses `os.CreateTemp` with random suffix to prevent symlink attacks.

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
### Resolved in 2026-07-11 (Round 32+33+34+36+37+38)

These were listed as debt in earlier versions and are now fixed:

- `vendorCircuitBreaker` package-level map data race → `sync.Map` (`protocol/retry.go`)
- `EventBus.stopped` data race (`RLock` read vs `Lock` write) → `atomic.Bool` (`protocol/stream.go`)
- ToolExecutor panic crashed entire process → `safeRunOne` with recover (`internal/adk/tool/executor.go`)
- Stream recovery error silently lost → `sw.Send(nil, err)` (`internal/server/chatmodel.go`)
- ReactAgent.Stream Recv errors silently dropped → error event propagation (`internal/adk/react.go`)
- `ValidateBaseURL` result ignored in `NewOpenAI` → returns error (`protocol/openai.go`)
- `Chain.Use()` data race on `middlewares` slice → `sync.RWMutex` + copy-on-read (`internal/adk/middleware/chain.go`)
- `safeInvoke` swallowed panics without trace → `log.Printf` (`internal/adk/event/bus.go`)
- 10 JSON marshal / response read errors silently discarded → proper error handling across 5 files
- `isPrivateIP` missed `0.0.0.0` (unspecified) → added `IsLoopback()`/`IsUnspecified()` check
- `sse_framer.go` (271 lines of dead code, `//go:build never`) → deleted
- Session storage no background eviction → `Manager.StartCleanup(ctx, interval, maxAge)` background goroutine (`internal/session/manager.go:74`)
- Vendor policy switch duplication across `openai.go`/`health_manager.go` → centralized in `protocol/vendor_policy.go`
- Vendor policy functions untested → `protocol/vendor_policy_test.go` (8 tests)
- `EventBus.Publish` backpressure blocked stream emit → `TryPublish` non-blocking send (`protocol/openai.go`)
- `UsageTracker.Record` DB write under exclusive lock blocked stats queries → prepared statement + `RLock` for read, `stMu.Lock()` for write (`protocol/usage_tracker.go`)
- `tool/registry.go` `Check()` TOCTOU race (`r.Get` outside lock) → `t.Check(ctx)` inside `r.mu.RLock()` (`internal/adk/tool/registry.go`)
- `compressCache` manual map+mutex+stale eviction (500 cap, batch-50 evict, no LRU) → `golang-lru/v2/expirable.NewLRU` with automatic LRU eviction and 30s TTL (`protocol/content_compress.go`)
- `CircuitBreaker` global `failureRate` field unused, `Success()` not resetting in Closed state, no constructor → `NewCircuitBreaker` with `FailureThreshold`, `Success()` resets counter in Closed, removed `failureRate` (`protocol/types.go`)
- `EventBus.Stop()` send-on-closed-channel panic → `runDone` channel synchronization: close publisher → wait runDone → close subs → wg.Wait (`protocol/stream.go`)
- `HealthChecker.onStatusChange`/`logf` dead code (logf always returned nil) → removed (`protocol/health_check.go`)
- `HealthManager.Stop()` not wired into graceful shutdown → `StopHealthManager()` called from `main.go` signal handler (`protocol/health_manager.go`, `main.go`)
- `CircuitBreaker.Success()` not called on stream success → `streamWithFailover` and `SendWithRetry` both call `Success()` after successful HTTP 200, allowing HalfOpen → Closed transition (`protocol/openai.go`, `protocol/retry.go`)
- `EventBus.dispatch` processor panic → `recover` + `log.Printf` prevents `wg.Wait()` deadlock in `Stop()` (`protocol/stream.go`)

---

## 11. ADK Integration Status

Completed:

- `internal/adk/` — full ADK layer implemented (tool, llm, middleware, event, session, types, convert, agent, react, runner)
- `internal/adk/convert/` — single source of truth for eino↔adk message conversion (`ToEino`/`FromEino`/`ToEinoSlice`/`FromEinoSlice`), replacing duplicated conversion logic across react/session/main
- `main.go` — consumes `adk.Runner` (wraps `Agent` + middleware + event bus) via `agentAdapter` → `server.AgentRunner`
- Middleware (4-hook onion chain) — `AroundModel` in `Runner.Run`, `AroundTool` in `ReactAgent` (wraps each eino tool `InvokableRun`)
- EventBus — `Runner` publishes `AgentStart`/`AgentEnd`/`Error` lifecycle events; handlers run panic-isolated (each wrapped in `recover`)
- Session `Store` — `Runner` auto-loads history and appends responses when `SessionID` is set
- Guardrails — checked at two layers: (1) `Runner.Run`/`Runner.Stream` entry checks request tool calls; (2) `middlewareWrappedTool.InvokableRun` checks each tool call the agent produces at runtime (blocks denied tools before execution)
- Security hardening — workspace root path validation (`WORKSPACE_ROOT`) enforced in `tools/fileops.go` and `internal/server/server.go`; `tools/vision.go` `fetchImage` rejects private/internal URLs unless `ALLOW_PRIVATE_IMAGE_URLS=true`; `tools/terminal.go` `runElevated` uses `os.CreateTemp` random temp files
- Graceful shutdown — `main.go` uses `http.Server.Shutdown()` with 10s timeout on SIGINT/SIGTERM; `bus.Stop()` removed (ADK event.Bus does not require explicit stop)
- Concurrency safety — `protocol/types.go` `CircuitBreaker.Check()` uses exclusive `Lock()` for state mutations; `protocol/stream.go` `Publish` uses non-blocking send to prevent producer backpressure
- Session message isolation — `internal/adk/runner.go` `Run`/`Stream` allocate new message slice instead of mutating caller's `req.Messages`
- Telemetry simplification — `protocol/telemetry.go` `RecordFromContext` streamlined to direct context extraction without redundant branching

**Stream 中间件限制**: `Runner.Stream` 只执行 middleware 的 `BeforeModel` 钩子（通过 `Chain.BeforeModelChain`）。`AfterModel` 需要完整的模型响应对象才能运作，而流式响应是消费时逐块产生的增量数据，无法在返回时提供，故不在流式路径执行。需要观测/改写完整响应的中间件应作用于非流式 `Run` 路径。`AgentEnd` 事件通过 `defer` 在流 goroutine 退出时兜底发布，保证与 `AgentStart` 配对（即使消费方提前断开或 ctx 取消）。

Do not do yet:

- Do not delete or rename `protocol/`.
- Do not move `tools/` to `internal/skill/`.
- Do not remove `reference/eino` replace.
- Do not run DSPy optimization in the online request path.
- Do not mix prompt optimization with RAW provider calling logic.


---

## 12. ADK Layer Target Architecture

> Target: introduce `internal/adk/` as the agent runtime orchestration layer between `protocol/` (RAW) and `internal/server` (HTTP).

### 12.1 Layer Position

```text
main.go
    |  (thin wiring)
    v
internal/server (HTTP handlers, SSE)
    |  (consumes adk.Agent interface)
    v
internal/adk/   ← NEW
    |  (agent runtime orchestration)
    +---> tool/     # ToolRegistry, Executor, Guardrails
    +---> llm/      # ChatModel bridge, Model resolution
    +---> session/  # Store interface
    +---> prompt/   # PromptProvider interface
    +---> middleware/  # 4-hook chain
    +---> event/    # Agent lifecycle events
    |
    +---> eino react.Agent  (wrapped, not replaced)
            |
            +---> tools/
            +---> chatModel  (via llm/bridge)
                    |
                    v
                protocol.Protocol (RAW layer, unchanged)
```

### 12.2 Package Structure

| Package | Contents | Status |
|---|---|---|
| `internal/adk/tool/` | Tool interface, ToolRegistry (check_fn TTL+grace), ToolExecutor (parallel+interrupt), Guardrails | Done |
| `internal/adk/llm/` | Bridge (eino ToolCallingChatModel adapter wrapping protocol), ModelRef resolution | Done |
| `internal/adk/session/` | Store interface (Get/Append/Snapshot) + ManagerAdapter wrapping `internal/session.Manager` | Done |
| `internal/adk/middleware/` | Middleware interface + Chain (BeforeModel/AfterModel/BeforeTool/AfterTool, onion model) | Done, integrated into Runner |
| `internal/adk/event/` | Event types + lightweight EventBus for agent lifecycle | Done, integrated into Runner |
| `internal/adk/types/` | ADK-native Message/ToolCall/Request/Response/Event (zero eino dependency) | Done |
| `internal/adk/agent.go` | Agent interface (Run/Stream) + AgentConfig | Done |
| `internal/adk/react.go` | ReAct loop — wraps eino react.Agent as adk.Agent | Done |
| `internal/adk/runner.go` | Runner — turn lifecycle with guardrails, middleware (AroundModel), event publishing, session store | Done |
| `internal/adk/prompt/` | PromptProvider interface + Manager (version management reserved for DSPy) | Not started — `PromptProvider` defined in `runner.go` and `server/interfaces.go` |

### 12.3 Key Design Decisions

1. **ReAct loop**: wraps eino `react.Agent`, no custom rewrite. ADK provides the interface layer; eino remains the underlying engine.
2. **ToolRegistry check_fn**: Hermes-style TTL cache (30s) + failure grace (60s). Transient failures within 60s of last success treated as flapping, return true.
3. **Session store**: `internal/session.Manager` implements `adk/session.Store` interface. Zero rewrite, pure interface adaptation.
4. **Middleware**: 4 hooks (BeforeModel/AfterModel/BeforeTool/AfterTool) with onion-shaped chain execution. `AroundModel` wired in Runner, `AroundTool` wired in ReactAgent via `middlewareWrappedTool` wrapping each eino tool's `InvokableRun`.
5. **Guardrails**: Hermes-style `check_fn` availability probing + allow/deny lists for fine-grained control.
6. **PromptProvider**: interface for stable prompt retrieval; Manager with version management reserved for future DSPy PromptLab.

### 12.4 Migration Phases

| Phase | Package | Contents | Status |
|---|---|---|---|
| A | `internal/adk/tool/` | Tool interface, Registry (check_fn TTL+grace), Executor, Guardrails | Done |
| B | `internal/adk/llm/` | Bridge (chatModel from internal/server), Model resolution | Done |
| C | `internal/adk/` | Agent interface + React (wraps eino) + Runner | Done |
| D | `internal/adk/middleware/` + `event/` | Middleware interface+chain + EventBus | Done |
| E | `internal/adk/session/` + `prompt/` | Store interface (Done), PromptProvider interface (defined in runner.go, NOT extracted to own package) | Mostly done |
| F | `main.go` + `internal/server` | Switch main.go to ADK interfaces (Runner, Bridge, ToolRegistry); server keeps eino `schema.Message` on HTTP boundary | Done |
| G | `internal/adk/runner.go` | Middleware AroundModel integrated into Runner.Run/Stream | Done |
| H | `internal/adk/runner.go` | EventBus AgentStart/AgentEnd/Error lifecycle events | Done |
| I | `internal/adk/runner.go` | Session Store auto-load/append in Runner | Done |
| J | `main.go` | agentAdapter wraps `*adk.Runner` instead of raw `adk.Agent` | Done |
| AroundTool | `internal/adk/react.go` | middlewareWrappedTool wraps each tool's InvokableRun with BeforeTool/AfterTool | Done |

### 12.5 Boundaries (Do Not Cross)

- ADK must NOT import `protocol/` types directly; use `llm/bridge` for conversion.
- ADK must NOT import `tools/*` directly; use `tool/registry` for dispatch.
- ADK must NOT own HTTP handlers (stay in `internal/server`).
- ADK must NOT own session persistence (stay in `internal/session`).
- ADK must NOT own RAW provider calling logic (stay in `protocol/`).
- Do not delete or rename `protocol/`.
- Do not move `tools/` to `internal/skill/`.
- Do not remove `reference/eino` replace.
- Do not run DSPy optimization in the online request path.
