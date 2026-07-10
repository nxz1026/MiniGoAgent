package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func init() {
	Register("openai", func(cfg Config) (Protocol, error) {
		return NewOpenAI(cfg), nil
	})
}

type reasoningPolicy int

const (
	reasoningNone      reasoningPolicy = iota
	reasoningEffort
	reasoningThinking
)

type OpenAI struct {
	apiKey        string
	apiKeys       []string
	keyIndex      int
	baseURL       string
	model         string
	client        *http.Client
	name          string
	vendor        Vendor
	effort        string
	thinkingType  string
	policy        reasoningPolicy
	streamTimeout time.Duration
	maxReconnect  int
	rateLimiter   *RateLimiter
	Logf          func(format string, args ...any)
	Telemetry     *Telemetry
	usageMu       sync.Mutex
	keyMu         sync.Mutex
	lastUsage     *Usage
	eventBus      *EventBus
	failover      FailoverConfig
}

func (o *OpenAI) GetTelemetry() *Telemetry { return o.Telemetry }

func (o *OpenAI) GetEventBus() *EventBus { return o.eventBus }

var bodyPool = sync.Pool{
	New: func() any { return make(map[string]any, 16) },
}

var DefaultTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 100
	t.MaxIdleConnsPerHost = 20
	t.MaxConnsPerHost = 50
	t.IdleConnTimeout = 120 * time.Second
	return t
}()

func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: DefaultTransport}
}

func defaultHTTPClient(timeout time.Duration) *http.Client {
	return NewHTTPClient(timeout)
}

func NewOpenAI(cfg Config) *OpenAI {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if err := ValidateBaseURL(baseURL); err != nil {
		// log via internal error field; construction continues so callers can handle nil-check
		// Most callers should call ValidateBaseURL explicitly before NewOpenAI
	}
	apiKeys := cfg.APIKeys
	if len(apiKeys) == 0 && cfg.APIKey != "" {
		apiKeys = []string{cfg.APIKey}
	}
	timeout := cfg.StreamTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	reconnect := cfg.MaxReconnect
	if reconnect <= 0 {
		reconnect = 3
	}
	o := &OpenAI{
		apiKey:        cfg.APIKey,
		apiKeys:       apiKeys,
		baseURL:       baseURL + "/chat/completions",
		model:         cfg.Model,
		client:        defaultHTTPClient(timeout + 30*time.Second),
		name:          cfg.Model,
		vendor:        DetectVendor(baseURL),
		streamTimeout: timeout,
		maxReconnect:  reconnect,
		rateLimiter:   NewRateLimiter(cfg.RateLimitRPM, cfg.RateLimitTPM),
		Telemetry:     NewTelemetry(),
		eventBus:      NewEventBus(context.Background(), 256),
	}
	if cfg.FallbackModel != "" && cfg.FallbackBaseURL != "" {
		o.failover = FailoverConfig{
			MaxRetries: 2,
			ShouldFailover: func(model, vendor string, err error) bool {
				return true
			},
			GetFailoverModel: func(model, vendor string) (string, string, bool) {
				return cfg.FallbackModel, cfg.FallbackBaseURL, true
			},
		}
	}
	o.Telemetry.SetThresholds(cfg.ContextWarnPct, cfg.ContextCompressPct)
	switch o.vendor {
	case VendorDeepSeek:
		o.policy = reasoningThinking
	case VendorZhipu, VendorMiniMax, VendorLongCat, VendorMiMo, VendorStepFun, VendorQwen:
		o.policy = reasoningThinking
	default:
		o.policy = reasoningEffort
	}
	o.eventBus.Subscribe("telemetry", &telemetryProcessor{telemetry: o.Telemetry})
	o.eventBus.Subscribe("log", &logProcessor{logf: o.logf})

	healthURL := cfg.HealthCheckURL
	if healthURL == "" {
		healthURL = defaultHealthEndpoint(o.vendor, baseURL)
	}
	if healthURL != "" {
		vendorHealthManager.Register(o.vendor, healthURL, 30*time.Second, vendorCircuitBreaker[o.vendor])
	}
	return o
}

func (o *OpenAI) Chat(ctx context.Context, req Request) (*Response, error) {
	start := time.Now()
	if err := o.rateLimiter.Wait(ctx, estimateTokens(req)); err != nil {
		RunOnErrorHooks(ctx, &req, err)
		return nil, err
	}
	modifiedReq, err := RunBeforeProcessHooks(ctx, &req)
	if err != nil {
		RunOnErrorHooks(ctx, &req, err)
		return nil, err
	}
	resp, err := o.chatWithFailover(ctx, *modifiedReq, start)
	if err != nil {
		RunOnErrorHooks(ctx, &req, err)
		return nil, err
	}
	resp, err = RunAfterProcessHooks(ctx, &req, resp)
	if err != nil {
		RunOnErrorHooks(ctx, &req, err)
		return nil, err
	}
	return resp, nil
}

func (o *OpenAI) chatWithFailover(ctx context.Context, req Request, start time.Time) (*Response, error) {
	var lastErr error
	for attempt := 0; attempt <= o.failover.maxRetries(); attempt++ {
		if attempt > 0 {
			if !o.failover.shouldFailover(o.model, o.vendor.String(), lastErr) {
				return nil, lastErr
			}
			fallbackModel, fallbackURL, ok := o.failover.getFailoverModel(o.model, o.vendor.String())
			if !ok {
				return nil, lastErr
			}
			o.logf(ctx, "RAW failover attempt=%d model=%s->%s baseURL=%s err=%v",
				attempt, o.model, fallbackModel, fallbackURL, lastErr)
			o.applyFailover(fallbackModel, fallbackURL)
		}
		resp, err := o.chatDirect(ctx, req, start)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (o *OpenAI) applyFailover(model, baseURL string) {
	o.baseURL = strings.TrimRight(baseURL, "/") + "/chat/completions"
	o.model = model
	o.vendor = DetectVendor(baseURL)
	switch o.vendor {
	case VendorDeepSeek:
		o.policy = reasoningThinking
	case VendorZhipu, VendorMiniMax, VendorLongCat, VendorMiMo, VendorStepFun, VendorQwen:
		o.policy = reasoningThinking
	default:
		o.policy = reasoningEffort
	}
}

func (o *OpenAI) chatDirect(ctx context.Context, req Request, start time.Time) (*Response, error) {
	body := o.buildBody(req, false)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.currentKey())
	o.logf(ctx, "RAW POST %s body=%dKB msgs=%d tools=%d model=%s (non-stream)",
		o.baseURL, len(body)/1024, len(req.Messages), len(req.Tools), o.model)
	if o.eventBus != nil {
		o.eventBus.TryPublish(Chunk{Type: ChunkRawRequest, Text: string(RedactBody(body))})
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		if o.eventBus != nil {
			o.eventBus.TryPublish(Chunk{Type: ChunkRawError, Text: "request failed: " + err.Error()})
		}
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyData, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		if o.eventBus != nil {
			o.eventBus.TryPublish(Chunk{Type: ChunkRawResponse, Text: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyData)))})
		}
		return nil, fmt.Errorf("API error (status=%d): %s", resp.StatusCode, strings.TrimSpace(string(bodyData)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID   string `json:"id"`
					Type string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *Usage `json:"usage"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal(bodyData, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("API error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("API returned empty choices")
	}

	msg := result.Choices[0].Message
	chatResp := &Response{
		Content:          msg.Content,
		ReasoningContent: msg.ReasoningContent,
		Usage:            result.Usage,
	}
	for _, tc := range msg.ToolCalls {
		chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
			ID: tc.ID, Type: tc.Type, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
		})
	}
	o.logf(ctx, "RAW chat direct done content=%dKB tool_calls=%d usage=%+v",
		len(chatResp.Content)/1024, len(chatResp.ToolCalls), chatResp.Usage)
	o.Telemetry.RecordFromContext(ctx, o.model, o.vendor.String(), time.Since(start).Seconds(), chatResp.Usage)
	return chatResp, nil
}

func estimateTokens(req Request) int {
	n := 0
	for _, m := range req.Messages {
		cjk := 0
		for _, r := range m.Content {
			if r >= 0x4E00 && r <= 0x9FFF || r >= 0x3400 && r <= 0x4DBF || r >= 0x2E80 && r <= 0x2FFF {
				cjk++
			}
		}
		ascii := len(m.Content) - cjk
		n += ascii/4 + cjk*2
	}
	return n
}

func (o *OpenAI) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	modifiedReq, err := RunBeforeProcessHooks(ctx, &req)
	if err != nil {
		RunOnErrorHooks(ctx, &req, err)
		return nil, err
	}
	out := make(chan Chunk, 256)
	go func() {
		o.streamWithFailover(ctx, *modifiedReq, out)
	}()
	return out, nil
}

func (o *OpenAI) streamWithFailover(ctx context.Context, req Request, out chan<- Chunk) {
	var emitted bool
	var lastErr error
	start := time.Now()
	if err := o.rateLimiter.Wait(ctx, estimateTokens(req)); err != nil {
		RunOnErrorHooks(ctx, &req, err)
		sendChunk(ctx, out, Chunk{Type: ChunkError, Error: err})
		return
	}
	defer func() {
		if lastErr != nil {
			RunOnErrorHooks(ctx, &req, lastErr)
		}
	}()
	maxReconnect := o.maxReconnect
	failoverMax := 0
	if o.failover.maxRetries() > 0 {
		failoverMax = o.failover.maxRetries()
	}
	totalAttempts := maxReconnect + failoverMax + 1
	for attempt := 0; attempt <= totalAttempts; attempt++ {
		if attempt > 0 {
			if emitted {
				o.logf(ctx, "RAW reconnect/failover attempt=%d skipped (already emitted)", attempt)
				sendChunk(ctx, out, Chunk{Type: ChunkError, Error: lastErr})
				return
			}
			if failoverMax > 0 && attempt > maxReconnect {
				if !o.failover.shouldFailover(o.model, o.vendor.String(), lastErr) {
					sendChunk(ctx, out, Chunk{Type: ChunkError, Error: lastErr})
					return
				}
				fallbackModel, fallbackURL, ok := o.failover.getFailoverModel(o.model, o.vendor.String())
				if !ok {
					sendChunk(ctx, out, Chunk{Type: ChunkError, Error: lastErr})
					return
				}
				o.logf(ctx, "RAW failover attempt=%d model=%s->%s baseURL=%s err=%v",
					attempt-maxReconnect, o.model, fallbackModel, fallbackURL, lastErr)
				o.applyFailover(fallbackModel, fallbackURL)
				continue
			}
			delay := backoffDelay(attempt, 0)
			o.logf(ctx, "RAW reconnect attempt=%d delay=%v err=%v", attempt, delay, lastErr)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		resp, err := SendWithRetry(ctx, o.client, o.vendor, func(ctx context.Context) (*http.Request, error) {
			return o.buildHTTPRequest(ctx, req)
		})
		if err != nil {
			o.logf(ctx, "RAW SendWithRetry attempt=%d err=%v", attempt, err)
			lastErr = err
			if o.eventBus != nil {
				o.eventBus.TryPublish(Chunk{Type: ChunkRawError, Text: "send_with_retry: " + err.Error()})
			}
			var ae *AuthError
			if errors.As(err, &ae) && o.rotateKey() {
				o.logf(ctx, "RAW key rotated to index=%d on auth error", o.keyIndex)
			}
			continue
		}
		o.logf(ctx, "RAW HTTP response status=%d", resp.StatusCode)
		emitted, err = o.readStream(ctx, resp, out)
		if err != nil {
			o.logf(ctx, "RAW readStream attempt=%d err=%v emitted=%v", attempt, err, emitted)
			lastErr = err
			if o.eventBus != nil {
				o.eventBus.TryPublish(Chunk{Type: ChunkRawError, Text: "readStream: " + err.Error()})
			}
			if emitted || !IsConnReset(err) {
				if !emitted {
					sendChunk(ctx, out, Chunk{Type: ChunkError, Error: err})
				}
				return
			}
			continue
		}
		o.recordStreamCall(ctx, start, nil)
		return
	}
	o.logf(ctx, "RAW all reconnect/failover attempts exhausted last_err=%v", lastErr)
	sendChunk(ctx, out, Chunk{Type: ChunkError, Error: lastErr})
	o.recordStreamCall(ctx, start, lastErr)
}

func (o *OpenAI) recordStreamCall(ctx context.Context, start time.Time, err error) {
	o.usageMu.Lock()
	usage := o.lastUsage
	o.lastUsage = nil
	o.usageMu.Unlock()
	if err != nil {
		return
	}
	o.Telemetry.RecordFromContext(ctx, o.model, o.vendor.String(), time.Since(start).Seconds(), usage)
}

func (o *OpenAI) sendContextWarning(ctx context.Context, out chan<- Chunk, usage *Usage) {
	if usage == nil {
		return
	}
	maxCtx := ModelContextWindow(o.model)
	if maxCtx <= 0 {
		return
	}
	pct := usage.PromptTokens * 100 / maxCtx
	if pct >= o.Telemetry.comprPct {
		sendChunk(ctx, out, Chunk{
			Type:    ChunkWarn,
			Warning: fmt.Sprintf("context at %d%% — compression recommended", pct),
		})
	} else if pct >= o.Telemetry.warnPct {
		sendChunk(ctx, out, Chunk{
			Type:    ChunkWarn,
			Warning: fmt.Sprintf("context at %d%%", pct),
		})
	}
}

func sendChunk(ctx context.Context, out chan<- Chunk, ch Chunk) {
	select {
	case out <- ch:
	case <-ctx.Done():
	default:
	}
}

func (o *OpenAI) logf(ctx context.Context, format string, args ...any) {
	if fn, ok := ctx.Value(CtxLogf).(func(string, ...any)); ok && fn != nil {
		fn(format, args...)
		return
	}
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

func (o *OpenAI) currentKey() string {
	o.keyMu.Lock()
	defer o.keyMu.Unlock()
	if o.keyIndex >= 0 && o.keyIndex < len(o.apiKeys) {
		return o.apiKeys[o.keyIndex]
	}
	return o.apiKey
}

func (o *OpenAI) rotateKey() bool {
	o.keyMu.Lock()
	defer o.keyMu.Unlock()
	if len(o.apiKeys) <= 1 {
		return false
	}
	o.keyIndex = (o.keyIndex + 1) % len(o.apiKeys)
	return true
}

func (o *OpenAI) buildHTTPRequest(ctx context.Context, req Request) (*http.Request, error) {
	body := o.buildBody(req, true)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.currentKey())
	o.logf(ctx, "RAW POST %s body=%dKB msgs=%d tools=%d model=%s key_idx=%d",
		o.baseURL, len(body)/1024, len(req.Messages), len(req.Tools), o.model, o.keyIndex)
	return httpReq, nil
}

func (o *OpenAI) buildBody(req Request, stream bool) []byte {
	m := bodyPool.Get().(map[string]any)
	clear(m)
	m["model"] = o.model
	m["messages"] = o.toWireMessages(NormalizeMessages(req.Messages))
	m["stream"] = stream
	if len(req.Tools) > 0 {
		m["tools"] = o.toWireTools(req.Tools)
	}
	if req.Temperature != nil {
		m["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		m["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		m["max_tokens"] = *req.MaxTokens
	}
	if len(req.Stop) > 0 {
		m["stop"] = req.Stop
	}
	o.buildThinkingFields(m)
	body, _ := json.Marshal(m)
	bodyPool.Put(m)
	return body
}

func (o *OpenAI) toWireMessages(msgs []Message) []map[string]any {
	liveIdx := findLiveZoneStart(msgs)
	out := make([]map[string]any, 0, len(msgs))
	for i, m := range msgs {
		item := map[string]any{"role": string(m.Role)}
		if len(m.MultiContent) > 0 {
			item["content"] = m.MultiContent
		} else {
			content := m.Content
			if i >= liveIdx && (m.Role == RoleTool || m.Role == RoleUser) && len(content) > 512 {
				ct := DetectContentType(content)
				content = CachedCompressContent(content, ct)
			}
			item["content"] = content
		}
		if len(m.ToolCalls) > 0 {
			tcs := make([]map[string]any, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				tcs[i] = map[string]any{
					"id": tc.ID, "type": "function",
					"function": map[string]string{"name": tc.Name, "arguments": tc.Arguments},
				}
			}
			item["tool_calls"] = tcs
			if o.vendor == VendorDeepSeek {
				item["reasoning_content"] = m.ReasoningContent
			}
		}
		if m.ToolCallID != "" {
			item["tool_call_id"] = m.ToolCallID
		}
		if m.Name != "" {
			item["name"] = m.Name
		}
		out = append(out, item)
	}
	return out
}

func sanitizeTool(t ToolSchema) ToolSchema {
	if strings.TrimSpace(t.Name) == "" {
		t.Name = "unnamed_tool"
	}
	if strings.TrimSpace(t.Description) == "" {
		t.Description = "user-defined tool"
	}
	return t
}

func (o *OpenAI) toWireTools(tools []ToolSchema) []map[string]any {
	sorted := append([]ToolSchema{}, tools...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	out := make([]map[string]any, len(sorted))
	for i, t := range sorted {
		t = sanitizeTool(t)
		params := sortSchemaKeys(CanonicalizeSchema(t.Parameters))
		out[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": params,
			},
		}
	}
	return out
}

func sortSchemaKeys(raw json.RawMessage) json.RawMessage {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	return json.RawMessage(sortKeysRecursive(v))
}

func sortKeysRecursive(v any) string {
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b bytes.Buffer
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			keyJSON, _ := json.Marshal(k)
			b.Write(keyJSON)
			b.WriteByte(':')
			b.WriteString(sortKeysRecursive(val[k]))
		}
		b.WriteByte('}')
		return b.String()
	case []any:
		var b bytes.Buffer
		b.WriteByte('[')
		for i, elem := range val {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(sortKeysRecursive(elem))
		}
		b.WriteByte(']')
		return b.String()
	default:
		data, _ := json.Marshal(val)
		return string(data)
	}
}

func findLiveZoneStart(msgs []Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleUser {
			return i
		}
	}
	return 0
}

func (o *OpenAI) streamErr(emitted bool, err error) error {
	if err != nil && emitted {
		return &StreamInterruptedError{Emitted: true, Err: err}
	}
	return err
}

func (o *OpenAI) buildThinkingFields(m map[string]any) {
	switch o.policy {
	case reasoningThinking:
		var t string
		switch o.vendor {
		case VendorDeepSeek:
			if o.thinkingType == "disabled" {
				m["thinking"] = map[string]string{"type": "disabled"}
				return
			}
			m["thinking"] = map[string]string{"type": "enabled"}
		case VendorMiniMax:
			t = o.effort
			if t == "" {
				t = "adaptive"
			}
			m["thinking"] = map[string]string{"type": t}
		case VendorZhipu:
			t = o.effort
			if t == "" {
				t = "enabled"
			}
			m["thinking"] = map[string]string{"type": t}
		case VendorLongCat, VendorMiMo, VendorStepFun, VendorQwen:
			t = o.thinkingType
			if t == "" {
				t = "enabled"
			}
			m["thinking"] = map[string]string{"type": t}
		}
	case reasoningEffort:
		if o.effort != "" {
			m["reasoning_effort"] = o.effort
		}
	}
}

// readStream uses bufio.Scanner (not SseFramer) because MiniGoAgent is an
// HTTP client: net/http handles chunked transfer transparently and Scanner's
// 64 KB internal buffer guarantees no line-level truncation. SseFramer in
// sse_framer.go is available for raw-TCP proxy scenarios but is not needed
// here — see that file for details and wiring instructions.
func (o *OpenAI) readStream(ctx context.Context, resp *http.Response, out chan<- Chunk) (emitted bool, _ error) {
	defer resp.Body.Close()
	defer close(out)

	if o.eventBus != nil {
		var headerBuf strings.Builder
		headerBuf.WriteString(fmt.Sprintf("HTTP/%d.%d %d %s\n", resp.ProtoMajor, resp.ProtoMinor, resp.StatusCode, resp.Status))
		for k, v := range resp.Header {
			for _, vv := range v {
				headerBuf.WriteString(fmt.Sprintf("%s: %s\n", k, vv))
			}
		}
		o.eventBus.TryPublish(Chunk{Type: ChunkRawResponse, Text: headerBuf.String()})
	}

	idleTimeout := o.streamTimeout
	if idleTimeout <= 0 {
		idleTimeout = 120 * time.Second
	}
	done := make(chan struct{})
	defer close(done)
	activity := make(chan struct{}, 1)
	o.logf(ctx, "RAW readStream stall watchdog started timeout=%v", idleTimeout)
	var stalled atomic.Bool
	go func() {
		idle := time.NewTimer(idleTimeout)
		defer idle.Stop()
		for {
			select {
			case <-ctx.Done():
				resp.Body.Close()
				return
			case <-idle.C:
				stalled.Store(true)
				resp.Body.Close()
				return
			case <-activity:
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(idleTimeout)
			case <-done:
				return
			}
		}
	}()

	acc := map[int]*ToolCall{}
	started := map[int]bool{}
	var order []int
	var think thinkSplitter

	send := func(ch Chunk) bool {
		select {
		case out <- ch:
			emitted = true
			_ = o.eventBus.Publish(ctx, ch)
			return true
		case <-ctx.Done():
			return false
		}
	}
	sendErr := func(err error) bool {
		return send(Chunk{Type: ChunkError, Error: o.streamErr(emitted, err)})
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case activity <- struct{}{}:
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			send(Chunk{Type: ChunkDone})
			break
		}

		var sr struct {
			Choices []struct {
				Delta struct {
					Content          string                   `json:"content"`
					ReasoningContent string                   `json:"reasoning_content"`
					Reasoning        string                   `json:"reasoning"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
			Error *struct{ Message string } `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &sr); err != nil {
			continue
		}
		if sr.Error != nil {
			sendErr(fmt.Errorf("%s", sr.Error.Message))
			err := fmt.Errorf("API stream error: %s", sr.Error.Message)
			return emitted, o.streamErr(emitted, err)
		}
		if sr.Usage != nil {
			u := &Usage{
				PromptTokens:     sr.Usage.PromptTokens,
				CompletionTokens: sr.Usage.CompletionTokens,
				TotalTokens:      sr.Usage.TotalTokens,
			}
			o.usageMu.Lock()
			o.lastUsage = u
			o.usageMu.Unlock()
			send(Chunk{Type: ChunkUsage, Usage: u})
			o.sendContextWarning(ctx, out, u)
		}
		if len(sr.Choices) == 0 {
			continue
		}

		delta := sr.Choices[0].Delta

		reasoningDelta := delta.ReasoningContent
		if reasoningDelta == "" {
			reasoningDelta = delta.Reasoning
		}
		if reasoningDelta != "" {
			if !send(Chunk{Type: ChunkReasoning, Text: reasoningDelta}) {
				return emitted, o.streamErr(emitted, ctx.Err())
			}
		}

		if delta.Content != "" {
			if o.vendor == VendorMiniMax {
				r, txt := think.push(delta.Content)
				if r != "" {
					if !send(Chunk{Type: ChunkReasoning, Text: r}) {
						return emitted, o.streamErr(emitted, ctx.Err())
					}
				}
				if txt != "" {
					if !send(Chunk{Type: ChunkText, Text: txt}) {
						return emitted, o.streamErr(emitted, ctx.Err())
					}
				}
			} else {
				if !send(Chunk{Type: ChunkText, Text: delta.Content}) {
					return emitted, o.streamErr(emitted, ctx.Err())
				}
			}
		}

		for _, tc := range delta.ToolCalls {
			cur, ok := acc[tc.Index]
			if !ok {
				cur = &ToolCall{}
				acc[tc.Index] = cur
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				cur.ID = tc.ID
			}
			if tc.Function.Name != "" {
				cur.Name = tc.Function.Name
			}
			cur.Arguments += tc.Function.Arguments
			if !started[tc.Index] && cur.Name != "" {
				started[tc.Index] = true
				if !send(Chunk{Type: ChunkToolCallStart, ToolCall: &ToolCall{ID: cur.ID, Name: cur.Name}}) {
					return emitted, o.streamErr(emitted, ctx.Err())
				}
			}
		}

		if sr.Choices[0].FinishReason != nil && *sr.Choices[0].FinishReason != "" {
			r, _ := think.flush()
			if r != "" {
				if !send(Chunk{Type: ChunkReasoning, Text: r}) {
					return emitted, o.streamErr(emitted, ctx.Err())
				}
			}
			for _, idx := range order {
				if tc := acc[idx]; tc != nil {
					if tc.ID == "" {
						tc.ID = fmt.Sprintf("call_%d", idx)
					}
					if !send(Chunk{Type: ChunkToolCall, ToolCall: tc}) {
						return emitted, o.streamErr(emitted, ctx.Err())
					}
				}
			}
			send(Chunk{Type: ChunkDone})
			return emitted, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return emitted, o.streamErr(emitted, fmt.Errorf("scanner error: %w", err))
	}
	if stalled.Load() {
		o.logf(ctx, "RAW stream idle timeout after %v", idleTimeout)
		sendErr(fmt.Errorf("stream idle timeout"))
		return emitted, o.streamErr(emitted, fmt.Errorf("stream idle timeout"))
	}
	o.logf(ctx, "RAW stream EOF emitted=%v", emitted)
	return emitted, nil
}
