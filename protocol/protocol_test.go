package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

	p := NewOpenAI("test-key", srv.URL, "test-model")
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

	p := NewOpenAI("test-key", srv.URL, "test-model")
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

	p := NewOpenAI("test-key", srv.URL, "test-model")
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

	p := NewOpenAI("test-key", srv.URL, "test-model")
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

	p := NewOpenAI("test-key", srv.URL, "test-model")
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
		sseHandler(w, r)
	}))
	defer srv.Close()

	p := NewOpenAI("test-key", srv.URL, "test-model")
	resp, err := p.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp.Content != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", resp.Content)
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
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"rate limited\"}}\n\n")
	}))
	defer srv.Close()

	p := NewOpenAI("test-key", srv.URL, "test-model")
	_, err := p.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected rate limit error, got %v", err)
	}
}

func TestTransientErr(t *testing.T) {
	if !transientErr(io.EOF) {
		t.Error("EOF should be transient")
	}
	if transientErr(context.Canceled) {
		t.Error("Canceled should NOT be transient")
	}
	if transientErr(nil) {
		t.Error("nil should NOT be transient")
	}
}
