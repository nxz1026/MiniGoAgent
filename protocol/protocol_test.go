package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestDetectVendor(t *testing.T) {
	tests := []struct {
		url    string
		want   Vendor
		reason string
	}{
		{"https://api.deepseek.com/v1", VendorDeepSeek, "标准 DeepSeek"},
		{"https://api.deepseek.com", VendorDeepSeek, "DeepSeek 无后缀"},
		{"https://eu.deepseek.com/v1", VendorDeepSeek, "DeepSeek 区域子域名"},
		{"https://open.bigmodel.cn/api", VendorZhipu, "智谱 open.bigmodel.cn"},
		{"https://api.z.ai/v1", VendorZhipu, "智谱国际 api.z.ai"},
		{"https://api.minimaxi.com/v1", VendorMiniMax, "MiniMax"},
		{"https://api.longcat.chat/v1", VendorLongCat, "LongCat"},
		{"https://api.ollama.com/v1", VendorOllamaCloud, "Ollama Cloud"},
		{"https://api.xiaomimimo.com/v1", VendorMiMo, "小米 MiMo"},
		{"https://api.stepfun.com/v1", VendorStepFun, "StepFun"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", VendorQwen, "阿里云 Qwen"},
		{"https://api.openai.com/v1", VendorUnspecified, "通用 OpenAI"},
		{"https://token.sensenova.cn/v1", VendorUnspecified, "自定义代理"},
		{"not-a-url", VendorUnspecified, "无效 URL"},
	}
	for _, tt := range tests {
		got := DetectVendor(tt.url)
		if got != tt.want {
			t.Errorf("DetectVendor(%q) = %d, want %d (%s)", tt.url, got, tt.want, tt.reason)
		}
	}
}

func TestNormalizeMessages(t *testing.T) {
	t.Run("healthy_chain_passthrough", func(t *testing.T) {
		msgs := []Message{
			{Role: RoleUser, Content: "hello"},
			{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{
				{ID: "call_1", Name: "terminal", Arguments: `{"cmd":"ls"}`},
			}},
			{Role: RoleTool, ToolCallID: "call_1", Name: "terminal", Content: "ok"},
		}
		got := NormalizeMessages(msgs)
		if len(got) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(got))
		}
	})

	t.Run("orphan_tool_dropped", func(t *testing.T) {
		msgs := []Message{
			{Role: RoleUser, Content: "hello"},
			{Role: RoleTool, ToolCallID: "call_x", Content: "orphan"},
		}
		got := NormalizeMessages(msgs)
		if len(got) != 1 {
			t.Fatalf("expected 1 message (orphan dropped), got %d", len(got))
		}
	})

	t.Run("missing_tool_result_backfilled", func(t *testing.T) {
		msgs := []Message{
			{Role: RoleUser, Content: "do it"},
			{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{
				{ID: "call_1", Name: "terminal", Arguments: `{"cmd":"ls"}`},
			}},
		}
		got := NormalizeMessages(msgs)
		if len(got) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(got))
		}
		if got[2].Content != InterruptedToolResult {
			t.Fatalf("expected placeholder, got %q", got[2].Content)
		}
	})

	t.Run("truncated_json_repaired", func(t *testing.T) {
		msgs := []Message{
			{Role: RoleUser, Content: "run tool"},
			{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{
				{ID: "call_1", Name: "terminal", Arguments: `{"cmd":"ls`},
			}},
			{Role: RoleTool, ToolCallID: "call_1", Name: "terminal", Content: "ok"},
		}
		got := NormalizeMessages(msgs)
		if len(got) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(got))
		}
		args := got[1].ToolCalls[0].Arguments
		if args != `{"cmd":"ls"}` {
			t.Fatalf("expected repaired JSON, got %q", args)
		}
	})

	t.Run("empty_name_backfilled", func(t *testing.T) {
		msgs := []Message{
			{Role: RoleUser, Content: "search"},
			{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{
				{ID: "call_1", Name: "", Arguments: `{}`},
			}},
			{Role: RoleTool, ToolCallID: "call_1", Name: "web_search", Content: "result"},
		}
		got := NormalizeMessages(msgs)
		if got[1].ToolCalls[0].Name != "web_search" {
			t.Fatalf("expected backfilled name 'web_search', got %q", got[1].ToolCalls[0].Name)
		}
	})
}

func TestCloseTruncatedJSON(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"cmd":"ls`, `{"cmd":"ls"}`},
		{`{"a":1,`, `{"a":1}`},
		{`{"a":[1,2`, `{"a":[1,2]}`},
		{`{"a":`, `{"a":null}`},
		{`{"a":"b"`, `{"a":"b"}`},
		{`{"a":1}`, `{"a":1}`},
		{``, `{}`},
	}
	for _, tt := range tests {
		got := closeTruncatedJSON(tt.input)
		if got != tt.want {
			t.Errorf("closeTruncatedJSON(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBackoffDelay(t *testing.T) {
	d := backoffDelay(1, 0)
	if d < 500*time.Millisecond || d > 750*time.Millisecond {
		t.Errorf("backoffDelay(1) = %v, expected ~500-750ms", d)
	}
	d2 := backoffDelay(6, 0)
	if d2 > maxBackoff+300*time.Millisecond {
		t.Errorf("backoffDelay(6) = %v, should be capped at ~%v", d2, maxBackoff)
	}
	d3 := backoffDelay(1, 5*time.Second)
	if d3 != 5*time.Second {
		t.Errorf("backoffDelay with retryAfter = %v, want 5s", d3)
	}
}

func TestRetryableStatus(t *testing.T) {
	if !retryableStatus(429) {
		t.Error("429 should be retryable")
	}
	if !retryableStatus(500) {
		t.Error("500 should be retryable")
	}
	if !retryableStatus(502) {
		t.Error("502 should be retryable")
	}
	if !retryableStatus(408) {
		t.Error("408 should be retryable")
	}
	if retryableStatus(400) {
		t.Error("400 should NOT be retryable")
	}
	if retryableStatus(401) {
		t.Error("401 should NOT be retryable")
	}
}

func TestThinkSplitter(t *testing.T) {
	t.Run("simple_think_tag", func(t *testing.T) {
		var s thinkSplitter
		r, txt := s.push("<think>internal reasoning</think>visible output")
		if r != "internal reasoning" {
			t.Fatalf("expected reasoning, got %q", r)
		}
		if txt != "visible output" {
			t.Fatalf("expected visible text, got %q", txt)
		}
	})

	t.Run("streaming_think_tag", func(t *testing.T) {
		var s thinkSplitter
		r, txt := s.push("<think>internal ")
		if r != "" || txt != "" {
			t.Fatalf("expected empty partial, got reasoning=%q text=%q", r, txt)
		}
		r, txt = s.push("reasoning</think>visible")
		if r != "internal reasoning" {
			t.Fatalf("expected combined reasoning, got %q", r)
		}
		if txt != "visible" {
			t.Fatalf("expected visible text, got %q", txt)
		}
	})

	t.Run("passthrough_no_think", func(t *testing.T) {
		var s thinkSplitter
		r, txt := s.push("hello world")
		if r != "" || txt != "hello world" {
			t.Fatalf("expected passthrough, got reasoning=%q text=%q", r, txt)
		}
	})
}

func TestIsConnReset(t *testing.T) {
	if !IsConnReset(io.EOF) {
		t.Error("EOF should be conn reset (premature stream end)")
	}
	if !IsConnReset(io.ErrUnexpectedEOF) {
		t.Error("ErrUnexpectedEOF should be conn reset")
	}
	if !IsConnReset(syscall.ECONNRESET) {
		t.Error("ECONNRESET should be conn reset")
	}
	if IsConnReset(context.Canceled) {
		t.Error("Canceled should not be conn reset")
	}
}

// ---------- mock-based Streaming tests ----------

func sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" World\"}}]}\n\n")
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}]}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func sseReasoningHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"step 1...\"}}]}\n\n")
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"42\"}}]}\n\n")
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}]}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func sseToolCallHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\":\\\"beijing\\\"}\"}}]}}]}\n\n")
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"tool_calls\"}]}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func sseDisconnectHandler(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, _ := hj.Hijack()
	conn.Close()
}

var sseAttempt int

func sseRetryHandler(w http.ResponseWriter, r *http.Request) {
	sseAttempt++
	if sseAttempt == 1 {
		// first attempt: disconnect
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
		return
	}
	// second attempt: succeed
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\n")
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}]}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func TestMockStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseHandler(w, r)
	}))
	defer srv.Close()

	p := 	NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"})
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var result string
	for chunk := range ch {
		if chunk.Type == ChunkText {
			result += chunk.Text
		}
	}
	if result != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", result)
	}
}

func TestMockStreamReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseReasoningHandler(w, r)
	}))
	defer srv.Close()

	p := 	NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"})
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "think step by step"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var text, reasoning string
	for chunk := range ch {
		switch chunk.Type {
		case ChunkText:
			text += chunk.Text
		case ChunkReasoning:
			reasoning += chunk.Text
		}
	}
	if reasoning != "step 1..." {
		t.Fatalf("expected reasoning 'step 1...', got %q", reasoning)
	}
	if text != "42" {
		t.Fatalf("expected text '42', got %q", text)
	}
}

func TestMockStreamToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseToolCallHandler(w, r)
	}))
	defer srv.Close()

	p := 	NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"})
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "weather"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var toolCalls []ToolCall
	for chunk := range ch {
		switch chunk.Type {
		case ChunkToolCall:
			toolCalls = append(toolCalls, *chunk.ToolCall)
		}
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Name != "get_weather" {
		t.Fatalf("expected tool 'get_weather', got %q", toolCalls[0].Name)
	}
}

func TestMockStreamReconnect(t *testing.T) {
	sseAttempt = 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseRetryHandler(w, r)
	}))
	defer srv.Close()

	p := 	NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"})
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var result string
	for chunk := range ch {
		if chunk.Type == ChunkText {
			result += chunk.Text
		}
	}
	if result != "recovered" {
		t.Fatalf("expected 'recovered', got %q", result)
	}
}

func TestMockDisconnectNoRetry(t *testing.T) {
	// disconnects after emitting -> no retry
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer srv.Close()

	p := 	NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"})
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var result string
	for chunk := range ch {
		if chunk.Type == ChunkText {
			result += chunk.Text
		}
	}
	if result != "partial" {
		t.Fatalf("expected 'partial', got %q", result)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call (no retry after emit), got %d", callCount)
	}
}

func TestMockChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Stream bool `json:"stream"`
		}
		json.Unmarshal(body, &req)
		if req.Stream {
			sseHandler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"Hello World"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"})
	resp, err := p.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp.Content != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", resp.Content)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 10 {
		t.Fatalf("expected usage with 10 prompt_tokens, got %+v", resp.Usage)
	}
}

// ---------- Schema canonicalization ----------

func TestCanonicalizeSchema(t *testing.T) {
	tests := []struct {
		input json.RawMessage
		want  string
	}{
		{json.RawMessage(nil), `{"properties":{},"type":"object"}`},
		{json.RawMessage{}, `{"properties":{},"type":"object"}`},
		{json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`), `{"properties":{"city":{"type":"string"}},"type":"object"}`},
		{json.RawMessage(`{"properties":{"x":{"type":"int"}}}`), `{"properties":{"x":{"type":"int"}},"type":"object"}`},
		{json.RawMessage(`invalid`), `{"properties":{},"type":"object"}`},
	}
	for _, tt := range tests {
		got := string(CanonicalizeSchema(tt.input))
		if got != tt.want {
			t.Errorf("CanonicalizeSchema(%v) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

// ---------- Factory registration ----------

func TestFactoryRegistration(t *testing.T) {
	Register("test_provider", func(cfg Config) (Protocol, error) {
		return nil, fmt.Errorf("test error for %s", cfg.Model)
	})
	defer delete(registry, "test_provider")

	_, err := New("test_provider", Config{Model: "m1"})
	if err == nil || !strings.Contains(err.Error(), "test error for m1") {
		t.Fatalf("expected test error, got %v", err)
	}

	_, err = New("nonexistent", Config{})
	if err == nil || !strings.Contains(err.Error(), "unknown provider kind") {
		t.Fatalf("expected unknown provider error, got %v", err)
	}
}

func TestOpenAIFactory(t *testing.T) {
	// verify "openai" is already registered via init()
	p, err := New("openai", Config{
		APIKey:  "test-key",
		BaseURL: "http://test.local",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("New openai failed: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil protocol")
	}
}

func TestMockStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Stream bool `json:"stream"`
		}
		json.Unmarshal(body, &req)
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, "data: {\"error\":{\"message\":\"rate limited\"}}\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"})
	_, err := p.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected rate limit error, got %v", err)
	}
}

func TestCircuitBreaker_Closed(t *testing.T) {
    cb := &CircuitBreaker{
        failureThreshold: 5,
        failureRate:      0.6,
        options: CircuitBreakerOptions{Timeout: time.Minute},
    }
    
    testErr := fmt.Errorf("test error")
    
    // 在closed状态下，<threshold次失败不会触发open
    for i := 0; i < 4; i++ {
        if !cb.Check(testErr) {
            t.Fatalf("expected success in closed state, got failure")
        }
    }
    
    // 第5次失败将触发状态转换为open
    if cb.Check(testErr) {
        t.Fatalf("expected closed->open transition")
    }
}

func TestCircuitBreaker_Open(t *testing.T) {
    now := time.Now()
    cb := &CircuitBreaker{
        failureThreshold: 5,
        failureRate:      0.6,
        lastFailure:     now,
        state:          Open,
        options: CircuitBreakerOptions{Timeout: time.Minute},
    }
    
    // open状态下，拒绝所有请求
    for i := 0; i < 3; i++ {
        if cb.Check(nil) {
            t.Fatalf("expected rejection in open state")
        }
    }
}

func TestCircuitBreaker_HalfOpen(t *testing.T) {
    now := time.Now()
    cb := &CircuitBreaker{
        failureThreshold: 5,
        failureRate:      0.6,
        lastFailure:     now.Add(-10 * time.Second), // 足够早
        state:          HalfOpen,
        options: CircuitBreakerOptions{Timeout: time.Minute, HalfOpenMaxCalls: 5},
    }
    
    // half-open状态允许后续请求
    if !cb.Check(nil) {
        t.Fatalf("expected success in half-open state")
    }
}

func TestStreamInterruptedError_Wrap(t *testing.T) {
	inner := fmt.Errorf("connection reset")
	e := &StreamInterruptedError{Emitted: true, Err: inner}
	if e.Error() == "" {
		t.Fatal("Error() returned empty")
	}
	if !strings.Contains(e.Error(), "stream interrupted") {
		t.Fatalf("expected 'stream interrupted' in error, got %q", e.Error())
	}
	if !strings.Contains(e.Error(), "connection reset") {
		t.Fatalf("expected 'connection reset' in error, got %q", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should unwrap to inner error")
	}
	var se *StreamInterruptedError
	if !errors.As(e, &se) {
		t.Error("errors.As should find StreamInterruptedError in chain")
	}
}

func TestStreamInterruptedError_MockStreamEmitThenDisconnect(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"part\"}}]}\n\n")
		w.(http.Flusher).Flush()
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"})
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var result string
	for chunk := range ch {
		if chunk.Type == ChunkText {
			result += chunk.Text
		}
	}
	if result != "part" {
		t.Fatalf("expected 'part', got %q", result)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call (no retry after emit), got %d", callCount)
	}
}

func TestStreamInterruptedError_MockStreamEmitThenAPIError(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"part\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"server stopped\"}}\n\n")
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"})
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var result string
	var chunkErr error
	for chunk := range ch {
		if chunk.Type == ChunkText {
			result += chunk.Text
		}
		if chunk.Type == ChunkError && chunk.Error != nil {
			chunkErr = chunk.Error
		}
	}
	if result != "part" {
		t.Fatalf("expected 'part', got %q", result)
	}
	if chunkErr == nil {
		t.Fatal("expected a chunk error after API error, got nil")
	}
	var se *StreamInterruptedError
	if !errors.As(chunkErr, &se) {
		t.Fatalf("expected StreamInterruptedError, got %T: %v", chunkErr, chunkErr)
	}
	if !se.Emitted {
		t.Fatal("StreamInterruptedError.Emitted should be true")
	}
}

func TestSanitizeTool(t *testing.T) {
	tests := []struct {
		input    ToolSchema
		name     string
		desc     string
	}{
		{ToolSchema{Name: "", Description: ""}, "unnamed_tool", "user-defined tool"},
		{ToolSchema{Name: "  ", Description: ""}, "unnamed_tool", "user-defined tool"},
		{ToolSchema{Name: "search", Description: ""}, "search", "user-defined tool"},
		{ToolSchema{Name: "", Description: "  desc  "}, "unnamed_tool", "  desc  "},
		{ToolSchema{Name: "calc", Description: "computes"}, "calc", "computes"},
	}
	for _, tt := range tests {
		got := sanitizeTool(tt.input)
		if got.Name != tt.name {
			t.Errorf("sanitizeTool name: got %q, want %q", got.Name, tt.name)
		}
		if got.Description != tt.desc {
			t.Errorf("sanitizeTool desc: got %q, want %q", got.Description, tt.desc)
		}
	}
}

func TestRetryNotifyContext(t *testing.T) {
	var notifyCalls []struct {
		attempt int
		max     int
	}
	notifyFn := func(attempt, max int) {
		notifyCalls = append(notifyCalls, struct{ attempt int; max int }{attempt, max})
	}
	ctx := context.WithValue(context.Background(), CtxRetryNotify, notifyFn)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		io.WriteString(w, `{"error":{"message":"unavailable"}}`)
	}))
	defer srv.Close()

	_, _ = SendWithRetry(ctx, NewHTTPClient(5*time.Second), VendorUnspecified,
		func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		})
	if len(notifyCalls) == 0 {
		t.Fatal("expected RetryNotify to be called at least once")
	}
	for _, call := range notifyCalls {
		if call.attempt < 1 || call.max != MaxRetries {
			t.Fatalf("unexpected notify call: attempt=%d max=%d", call.attempt, call.max)
		}
	}
}

func TestSendWithRetry_CircuitBreakerIntegration(t *testing.T) {
	cb := &CircuitBreaker{
		failureThreshold: 3,
		failureRate:      1.0,
		options:          CircuitBreakerOptions{Timeout: 5 * time.Minute},
	}
	vendorCircuitBreaker[VendorUnspecified] = cb
	defer delete(vendorCircuitBreaker, VendorUnspecified)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		io.WriteString(w, `{"error":{"message":"unavailable"}}`)
	}))
	defer srv.Close()

	_, err := SendWithRetry(context.Background(), NewHTTPClient(5*time.Second), VendorUnspecified,
		func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		})
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if cb.state != Open {
		t.Fatalf("expected circuit breaker Open after failures, got state=%v", cb.state)
	}
}

func TestSendWithRetry_CircuitBreakerAuthSuccess(t *testing.T) {
	cb := &CircuitBreaker{
		failureThreshold: 3,
		failureRate:      1.0,
		state:           HalfOpen,
		options:          CircuitBreakerOptions{Timeout: 5 * time.Minute},
	}
	vendorCircuitBreaker[VendorUnspecified] = cb
	defer delete(vendorCircuitBreaker, VendorUnspecified)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"message":"unauthorized"}}`)
	}))
	defer srv.Close()

	_, err := SendWithRetry(context.Background(), NewHTTPClient(5*time.Second), VendorUnspecified,
		func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		})
	if err == nil {
		t.Fatal("expected AuthError")
	}
	if cb.state != Closed {
		t.Fatalf("expected circuit breaker Closed after auth success reset, got state=%v", cb.state)
	}
}

func TestBodyPoolReuse(t *testing.T) {
	o := NewOpenAI(Config{APIKey: "k", BaseURL: "http://example.com/v1", Model: "m"})
	req := Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}}
	first := o.buildBody(req, false)
	second := o.buildBody(req, false)
	if string(first) != string(second) {
		t.Fatalf("expected identical bodies from pool, got %s vs %s", first, second)
	}
}

func TestDefaultTransportParams(t *testing.T) {
	transport := DefaultTransport
	if transport == nil {
		t.Fatal("DefaultTransport is nil")
	}
	if transport.MaxIdleConns != 100 {
		t.Fatalf("MaxIdleConns=%d, want 100", transport.MaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost != 20 {
		t.Fatalf("MaxIdleConnsPerHost=%d, want 20", transport.MaxIdleConnsPerHost)
	}
	if transport.MaxConnsPerHost != 50 {
		t.Fatalf("MaxConnsPerHost=%d, want 50", transport.MaxConnsPerHost)
	}
	if transport.IdleConnTimeout != 120*time.Second {
		t.Fatalf("IdleConnTimeout=%v, want 120s", transport.IdleConnTimeout)
	}
}

func TestEventBus_SubscribePublish(t *testing.T) {
	eb := NewEventBus(context.Background(), 16)
	var got []Chunk
	var mu sync.Mutex
	processed := make(chan struct{}, 2)
	eb.Subscribe("s1", &testProcessor{chunks: &got, mu: &mu, done: processed})
	_ = eb.Publish(context.Background(), Chunk{Type: ChunkText, Text: "hello"})
	_ = eb.Publish(context.Background(), Chunk{Type: ChunkDone})
	time.Sleep(100 * time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(got))
	}
	if got[0].Text != "hello" {
		t.Fatalf("expected hello, got %s", got[0].Text)
	}
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	eb := NewEventBus(context.Background(), 16)
	var s1, s2 []Chunk
	var mu1, mu2 sync.Mutex
	done := make(chan struct{}, 2)
	eb.Subscribe("s1", &testProcessor{chunks: &s1, mu: &mu1, done: done})
	eb.Subscribe("s2", &testProcessor{chunks: &s2, mu: &mu2, done: done})
	_ = eb.Publish(context.Background(), Chunk{Type: ChunkText, Text: "broadcast"})
	time.Sleep(100 * time.Millisecond)
	if len(s1) != 1 || len(s2) != 1 {
		t.Fatalf("expected both subscribers to receive 1 chunk, got s1=%d s2=%d", len(s1), len(s2))
	}
}

type testProcessor struct {
	chunks *[]Chunk
	mu     *sync.Mutex
	done   chan struct{}
}

func (p *testProcessor) Process(_ context.Context, chunk Chunk) error {
	if p.chunks != nil && p.mu != nil {
		p.mu.Lock()
		*p.chunks = append(*p.chunks, chunk)
		p.mu.Unlock()
	}
	if p.done != nil {
		select {
		case p.done <- struct{}{}:
		default:
		}
	}
	return nil
}

func (p *testProcessor) Name() string { return "test" }

func TestContentPredictor_CacheHit(t *testing.T) {
	p := NewContentPredictor(true)
	ct := p.Predict("hello world")
	ct2 := p.Predict("hello world")
	if ct != ct2 {
		t.Fatalf("expected same content type from cache, got %d vs %d", ct, ct2)
	}
}

func TestContentPredictor_CacheMiss(t *testing.T) {
	p := NewContentPredictor(true)
	ct1 := p.Predict("[1, 2, 3]")
	ct2 := p.Predict("diff --git a/foo b/foo")
	if ct1 == ct2 {
		t.Fatalf("expected different content types for different inputs, got %d vs %d", ct1, ct2)
	}
}

func TestHealthChecker_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cb := &CircuitBreaker{failureThreshold: 3, failureRate: 1.0, options: CircuitBreakerOptions{Timeout: time.Minute}}
	checker := NewHealthChecker(VendorUnspecified, srv.URL, 30*time.Second, cb)
	checker.check(context.Background())

	checker.mu.RLock()
	status := checker.status
	checker.mu.RUnlock()
	if status != HealthHealthy {
		t.Fatalf("expected HealthHealthy, got %v", status)
	}
}

func TestHealthChecker_Unhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cb := &CircuitBreaker{failureThreshold: 3, failureRate: 1.0, options: CircuitBreakerOptions{Timeout: time.Minute}}
	checker := NewHealthChecker(VendorUnspecified, srv.URL, 30*time.Second, cb)
	checker.check(context.Background())

	checker.mu.RLock()
	status := checker.status
	checker.mu.RUnlock()
	if status != HealthUnhealthy {
		t.Fatalf("expected HealthUnhealthy, got %v", status)
	}
}

func TestHealthManager_RegisterGetStatus(t *testing.T) {
	hm := NewHealthManager(context.Background())
	defer hm.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cb := &CircuitBreaker{failureThreshold: 3, failureRate: 1.0, options: CircuitBreakerOptions{Timeout: time.Minute}}
	hm.Register(VendorUnspecified, srv.URL, 30*time.Second, cb)

	status := hm.GetStatus(VendorUnspecified)
	if status != HealthHealthy && status != HealthUnknown {
		t.Fatalf("expected HealthHealthy or HealthUnknown, got %v", status)
	}
}

func TestSendWithRetry_HealthShortCircuit(t *testing.T) {
	hm := NewHealthManager(context.Background())
	defer hm.Stop()

	cb := &CircuitBreaker{failureThreshold: 3, failureRate: 1.0, state: HalfOpen, options: CircuitBreakerOptions{Timeout: time.Minute}}
	vendorHealthManager = hm
	vendorCircuitBreaker[VendorUnspecified] = cb
	defer func() {
		vendorHealthManager = NewHealthManager(context.Background())
		delete(vendorCircuitBreaker, VendorUnspecified)
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	hm.Register(VendorUnspecified, srv.URL, 30*time.Second, cb)
	hm.setCheckerStatus(VendorUnspecified, HealthCircuitOpen)

	_, err := SendWithRetry(context.Background(), NewHTTPClient(5*time.Second), VendorUnspecified,
		func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		})
	if err == nil || !strings.Contains(err.Error(), "unhealthy") {
		t.Fatalf("expected unhealthy error, got %v", err)
	}
}

// ---------- buildThinkingFields + vendor-specific streaming ----------

func TestBuildThinkingFields(t *testing.T) {
	tests := []struct {
		vendor    Vendor
		effort    string
		thinkType string
		want      map[string]any
	}{
		{VendorDeepSeek, "", "", map[string]any{"thinking": map[string]string{"type": "enabled"}}},
		{VendorDeepSeek, "", "disabled", map[string]any{"thinking": map[string]string{"type": "disabled"}}},
		{VendorMiniMax, "", "", map[string]any{"thinking": map[string]string{"type": "adaptive"}}},
		{VendorMiniMax, "high", "", map[string]any{"thinking": map[string]string{"type": "high"}}},
		{VendorZhipu, "", "", map[string]any{"thinking": map[string]string{"type": "enabled"}}},
		{VendorZhipu, "low", "", map[string]any{"thinking": map[string]string{"type": "low"}}},
		{VendorLongCat, "", "", map[string]any{"thinking": map[string]string{"type": "enabled"}}},
		{VendorLongCat, "", "disabled", map[string]any{"thinking": map[string]string{"type": "disabled"}}},
		{VendorMiMo, "", "", map[string]any{"thinking": map[string]string{"type": "enabled"}}},
		{VendorOllamaCloud, "medium", "", map[string]any{"reasoning_effort": "medium"}},
		{VendorUnspecified, "low", "", map[string]any{"reasoning_effort": "low"}},
	}
	for _, tt := range tests {
		o := &OpenAI{
			vendor:       tt.vendor,
			policy:       reasoningThinking,
			effort:       tt.effort,
			thinkingType: tt.thinkType,
		}
		m := make(map[string]any)
		if tt.vendor == VendorOllamaCloud || tt.vendor == VendorUnspecified {
			o.policy = reasoningEffort
		}
		o.buildThinkingFields(m)
		gotJSON, _ := json.Marshal(m)
		wantJSON, _ := json.Marshal(tt.want)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("vendor=%s: got %s, want %s", tt.vendor, gotJSON, wantJSON)
		}
	}
}

func vendorStreamHandler(v Vendor, field string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if field != "" {
			var req map[string]any
			if err := json.Unmarshal(body, &req); err == nil {
				msgs, _ := req["messages"].([]any)
				if len(msgs) > 0 {
					_ = msgs[0]
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		switch v {
		case VendorMiniMax:
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"<think>reasoning</think>answer\"}}]}\n\n")
		default:
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\",\"content\":\"answer\"}}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

func TestMockStream_DeepSeek(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\",\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "deepseek-chat"})
	p.vendor = VendorDeepSeek
	p.policy = reasoningThinking
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var text, reasoning string
	for chunk := range ch {
		switch chunk.Type {
		case ChunkText:
			text += chunk.Text
		case ChunkReasoning:
			reasoning += chunk.Text
		}
	}
	if text != "hi" {
		t.Fatalf("expected text 'hi', got %q", text)
	}
	if reasoning != "think" {
		t.Fatalf("expected reasoning 'think', got %q", reasoning)
	}
}

func TestMockStream_MiniMax(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"<think>reasoning</think>answer\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "minimax/MiniMax-M1-80k"})
	p.vendor = VendorMiniMax
	p.policy = reasoningThinking
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var text, reasoning string
	for chunk := range ch {
		switch chunk.Type {
		case ChunkText:
			text += chunk.Text
		case ChunkReasoning:
			reasoning += chunk.Text
		}
	}
	if text != "answer" {
		t.Fatalf("expected text 'answer', got %q", text)
	}
	if reasoning != "reasoning" {
		t.Fatalf("expected reasoning 'reasoning', got %q", reasoning)
	}
}

func TestMockStream_Zhipu(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"zh-reason\",\"content\":\"zh-answer\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "glm-4"})
	p.vendor = VendorZhipu
	p.policy = reasoningThinking
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var text, reasoning string
	for chunk := range ch {
		switch chunk.Type {
		case ChunkText:
			text += chunk.Text
		case ChunkReasoning:
			reasoning += chunk.Text
		}
	}
	t.Logf("Zhipu text=%q reasoning=%q", text, reasoning)
	if text != "zh-answer" {
		t.Fatalf("expected text 'zh-answer', got %q", text)
	}
	if reasoning != "zh-reason" {
		t.Fatalf("expected reasoning 'zh-reason', got %q", reasoning)
	}
}

func TestMockStream_LongCat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"lc-reason\",\"content\":\"lc-answer\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "longcat-chat"})
	p.vendor = VendorLongCat
	p.policy = reasoningThinking
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var text, reasoning string
	for chunk := range ch {
		switch chunk.Type {
		case ChunkText:
			text += chunk.Text
		case ChunkReasoning:
			reasoning += chunk.Text
		}
	}
	if text != "lc-answer" {
		t.Fatalf("expected text 'lc-answer', got %q", text)
	}
	if reasoning != "lc-reason" {
		t.Fatalf("expected reasoning 'lc-reason', got %q", reasoning)
	}
}

func TestMockStream_MiMo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"mimo-reason\",\"content\":\"mimo-answer\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "mimo-chat"})
	p.vendor = VendorMiMo
	p.policy = reasoningThinking
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var text, reasoning string
	for chunk := range ch {
		switch chunk.Type {
		case ChunkText:
			text += chunk.Text
		case ChunkReasoning:
			reasoning += chunk.Text
		}
	}
	if text != "mimo-answer" {
		t.Fatalf("expected text 'mimo-answer', got %q", text)
	}
	if reasoning != "mimo-reason" {
		t.Fatalf("expected reasoning 'mimo-reason', got %q", reasoning)
	}
}

func TestMockStream_OllamaCloud(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"ollama-reason\",\"content\":\"ollama-answer\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "llama3.1"})
	p.vendor = VendorOllamaCloud
	p.policy = reasoningEffort
	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var text, reasoning string
	for chunk := range ch {
		switch chunk.Type {
		case ChunkText:
			text += chunk.Text
		case ChunkReasoning:
			reasoning += chunk.Text
		}
	}
	if text != "ollama-answer" {
		t.Fatalf("expected text 'ollama-answer', got %q", text)
	}
	if reasoning != "ollama-reason" {
		t.Fatalf("expected reasoning 'ollama-reason', got %q", reasoning)
	}
}

func TestUsageTracker_NewRecordQuery(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "usage.db")
	ut, err := NewUsageTracker(dbPath)
	if err != nil {
		t.Fatalf("NewUsageTracker: %v", err)
	}
	defer ut.Close()

	if err := ut.Record("sess-1", "gpt-4", "openai", 12.5, 1000, 200); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := ut.Record("sess-1", "gpt-4", "openai", 8.2, 500, 100); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := ut.Record("sess-2", "claude-3", "anthropic", 15.0, 2000, 500); err != nil {
		t.Fatalf("Record: %v", err)
	}

	records, err := ut.Query(UsageQuery{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Query sess-1: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records for sess-1, got %d", len(records))
	}

	records, err = ut.Query(UsageQuery{Model: "claude-3"})
	if err != nil {
		t.Fatalf("Query model: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record for claude-3, got %d", len(records))
	}

	stats, err := ut.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TotalCalls != 3 {
		t.Fatalf("expected 3 total calls, got %d", stats.TotalCalls)
	}
	if stats.TotalTokens <= 0 {
		t.Fatalf("expected positive total tokens, got %d", stats.TotalTokens)
	}
}

func TestTelemetry_SetTrackerRecords(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "telemetry_usage.db")
	ut, err := NewUsageTracker(dbPath)
	if err != nil {
		t.Fatalf("NewUsageTracker: %v", err)
	}
	defer ut.Close()

	tel := NewTelemetry()
	tel.SetTracker(ut)
	tel.SetSessionID("test-session")

	warn, comp := tel.Record("gpt-4", "openai", 5.0, &Usage{PromptTokens: 500, CompletionTokens: 100})
	if warn {
		t.Error("unexpected warn")
	}
	if comp {
		t.Error("unexpected compress")
	}

	warn, comp = tel.Record("gpt-4", "openai", 10.0, &Usage{PromptTokens: 5000, CompletionTokens: 300})
	if !warn {
		t.Error("expected warn for 61% context")
	}

	stats, err := ut.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TotalCalls != 2 {
		t.Fatalf("expected 2 total calls via tracker, got %d", stats.TotalCalls)
	}
	if stats.TotalPrompt != 5500 {
		t.Fatalf("expected 5500 total prompt tokens, got %d", stats.TotalPrompt)
	}

	records, err := ut.Query(UsageQuery{SessionID: "test-session"})
	if err != nil {
		t.Fatalf("Query session: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
}

func TestUsageTracker_DailyModelVendorStats(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agg.db")
	ut, err := NewUsageTracker(dbPath)
	if err != nil {
		t.Fatalf("NewUsageTracker: %v", err)
	}
	defer ut.Close()

	ut.Record("s1", "gpt-4", "openai", 5.0, 1000, 200)
	ut.Record("s1", "gpt-4", "openai", 3.0, 500, 100)
	ut.Record("s2", "claude-3", "anthropic", 10.0, 2000, 500)

	daily, err := ut.GetDailyStats("", "")
	if err != nil {
		t.Fatalf("GetDailyStats: %v", err)
	}
	if len(daily) == 0 {
		t.Fatal("expected at least 1 daily stat")
	}
	if daily[0].CallCount != 3 {
		t.Fatalf("expected 3 total calls in daily stats, got %d", daily[0].CallCount)
	}

	models, err := ut.GetModelStats()
	if err != nil {
		t.Fatalf("GetModelStats: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 model stats, got %d", len(models))
	}

	vendors, err := ut.GetVendorStats()
	if err != nil {
		t.Fatalf("GetVendorStats: %v", err)
	}
	if len(vendors) != 2 {
		t.Fatalf("expected 2 vendor stats, got %d", len(vendors))
	}
}

func TestUsageTracker_QueryPagination(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pagination.db")
	ut, err := NewUsageTracker(dbPath)
	if err != nil {
		t.Fatalf("NewUsageTracker: %v", err)
	}
	defer ut.Close()

	for i := 0; i < 10; i++ {
		ut.Record("batch", "model-x", "vendor-y", float64(i), i*100, i*10)
	}

	all, _ := ut.Query(UsageQuery{Limit: 100})
	if len(all) != 10 {
		t.Fatalf("expected 10 records, got %d", len(all))
	}

	first, _ := ut.Query(UsageQuery{Limit: 3, Offset: 0})
	if len(first) != 3 {
		t.Fatalf("expected 3 first-page records, got %d", len(first))
	}

	second, _ := ut.Query(UsageQuery{Limit: 3, Offset: 3})
	if len(second) != 3 {
		t.Fatalf("expected 3 second-page records, got %d", len(second))
	}
	if first[0].ID == second[0].ID {
		t.Fatal("first and second page share same record")
	}
}

