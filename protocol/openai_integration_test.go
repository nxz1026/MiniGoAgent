//go:build integration

package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func chatModel(t *testing.T) Protocol {
	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if apiKey == "" || baseURL == "" {
		t.Skip("SKIP: 需要设置 OPENAI_API_KEY 和 OPENAI_BASE_URL")
	}
	return NewOpenAI(Config{APIKey: apiKey, BaseURL: baseURL, Model: getEnv("OPENAI_MODEL", "deepseek-v4-flash")})
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestOpenAIChat(t *testing.T) {
	p := chatModel(t)
	req := Request{
		Messages: []Message{
			{Role: RoleUser, Content: "hello, say hi back in one word"},
		},
	}
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	t.Logf("Response: %s", resp.Content)
	t.Logf("Reasoning: %s", resp.ReasoningContent)
	if resp.Content == "" {
		t.Fatal("empty response content")
	}
}

func TestOpenAIStream(t *testing.T) {
	p := chatModel(t)
	req := Request{
		Messages: []Message{
			{Role: RoleUser, Content: "count from 1 to 5, one per line"},
		},
	}
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var full, reasoning strings.Builder
	toolCalls := 0
	for chunk := range ch {
		switch chunk.Type {
		case ChunkText:
			full.WriteString(chunk.Text)
			t.Logf("TEXT: %q", chunk.Text)
		case ChunkReasoning:
			reasoning.WriteString(chunk.Text)
			t.Logf("REASON: %q", chunk.Text)
		case ChunkToolCallStart:
			t.Logf("TOOL_START: %s/%s", chunk.ToolCall.ID, chunk.ToolCall.Name)
		case ChunkToolCall:
			toolCalls++
			t.Logf("TOOL: %s/%s args=%s", chunk.ToolCall.ID, chunk.ToolCall.Name, chunk.ToolCall.Arguments)
		case ChunkUsage:
			t.Logf("USAGE: prompt=%d completion=%d total=%d", chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens, chunk.Usage.TotalTokens)
		case ChunkDone:
			t.Log("DONE")
		case ChunkError:
			if chunk.Error != nil {
				t.Errorf("STREAM ERROR: %v", chunk.Error)
			}
		}
	}
	if full.Len() == 0 && toolCalls == 0 {
		t.Fatal("no output received from stream")
	}
	t.Logf("Full text: %s", full.String())
	if reasoning.Len() > 0 {
		t.Logf("Reasoning: %s", reasoning.String())
	}
}

func TestOpenAITools(t *testing.T) {
	p := chatModel(t)
	req := Request{
		Messages: []Message{
			{Role: RoleUser, Content: "what time is it? use the clock tool"},
		},
		Tools: []ToolSchema{
			{
				Name:        "clock",
				Description: "get the current date and time",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"format": {
							"type": "string",
							"enum": ["iso", "unix"],
							"description": "output format"
						}
					}
				}`),
			},
		},
	}
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if len(resp.ToolCalls) == 0 {
		t.Fatal("expected tool call, got none")
	}
	for _, tc := range resp.ToolCalls {
		t.Logf("ToolCall: %s/%s args=%s", tc.ID, tc.Name, tc.Arguments)
	}
}

func TestOpenAIBadAuth(t *testing.T) {
	p := NewOpenAI(Config{APIKey: "bad-key", BaseURL: os.Getenv("OPENAI_BASE_URL"), Model: "gpt-4"})
	req := Request{
		Messages: []Message{
			{Role: RoleUser, Content: "hi"},
		},
	}
	_, err := p.Chat(context.Background(), req)
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	t.Logf("Expected auth error: %v", err)
}

func TestOpenAIBadModel(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if apiKey == "" || baseURL == "" {
		t.Skip("SKIP: 需要设置 OPENAI_API_KEY 和 OPENAI_BASE_URL")
	}
	p := NewOpenAI(Config{APIKey: apiKey, BaseURL: baseURL, Model: "nonexistent-model-xyz"})
	req := Request{
		Messages: []Message{
			{Role: RoleUser, Content: "hi"},
		},
	}
	_, err := p.Chat(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for bad model, got nil")
	}
	t.Logf("Expected model error: %v", err)
}

func TestOpenAIVendorDetection(t *testing.T) {
	tests := []struct {
		baseURL string
		vendor  Vendor
	}{
		{"https://api.deepseek.com/v1", VendorDeepSeek},
		{"https://open.bigmodel.cn/api", VendorZhipu},
		{"https://api.minimaxi.com/v1", VendorMiniMax},
		{"https://api.longcat.chat/v1", VendorLongCat},
		{"https://api.openai.com/v1", VendorUnspecified},
	}
	for _, tt := range tests {
		p := NewOpenAI(Config{APIKey: "test-key", BaseURL: tt.baseURL, Model: "test-model"})
		if p.vendor != tt.vendor {
			t.Errorf("NewOpenAI(%q).vendor = %d, want %d", tt.baseURL, p.vendor, tt.vendor)
		}
		t.Logf("baseURL=%s → vendor=%d", tt.baseURL, p.vendor)
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}

func TestOpenAIReasoning(t *testing.T) {
	p := chatModel(t)
	openai, ok := p.(*OpenAI)
	if !ok {
		t.Skip("SKIP: not *OpenAI")
	}
	if openai.vendor != VendorDeepSeek && openai.vendor != VendorZhipu {
		t.Skipf("SKIP: 供应商 %d 不支持 reasoning 测试", openai.vendor)
	}
	req := Request{
		Messages: []Message{
			{Role: RoleUser, Content: "solve 23 * 47 step by step"},
		},
	}
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	t.Logf("Content: %s", resp.Content)
	t.Logf("Reasoning content found: %v (len=%d)", resp.ReasoningContent != "", len(resp.ReasoningContent))
	if resp.ReasoningContent == "" {
		t.Log("WARNING: no reasoning content returned (API may not support it)")
	}
}

func printUsage(t *testing.T) {
	t.Log()
	t.Log("用法:")
	t.Log("  set OPENAI_API_KEY=xxx OPENAI_BASE_URL=https://...")
	t.Log(`  go test -tags=integration -run TestOpenAIChat ./protocol/ -v`)
	t.Log()
	t.Log("当前环境:")
	t.Logf("  OPENAI_API_KEY=%s", mask(os.Getenv("OPENAI_API_KEY"), 8))
	t.Logf("  OPENAI_BASE_URL=%s", os.Getenv("OPENAI_BASE_URL"))
	t.Logf("  OPENAI_MODEL=%s", os.Getenv("OPENAI_MODEL"))
}

func mask(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func TestOpenAIDebug(t *testing.T) {
	printUsage(t)
	if os.Getenv("OPENAI_API_KEY") == "" || os.Getenv("OPENAI_BASE_URL") == "" {
		t.Skip("SKIP: 未设置环境变量")
	}
	t.Log("环境变量已设置，可以运行其他测试")
}

func TestOpenAISendWithRetry(t *testing.T) {
	p, ok := chatModel(t).(*OpenAI)
	if !ok {
		t.Skip("SKIP: not *OpenAI")
	}

	ctx := context.Background()
	req := Request{
		Messages: []Message{
			{Role: RoleUser, Content: "say OK"},
		},
	}

	resp, err := SendWithRetry(ctx, p.client, p.vendor, func(ctx context.Context) (*http.Request, error) {
		return p.buildHTTPRequest(ctx, req)
	})
	if err != nil {
		t.Fatalf("SendWithRetry failed: %v", err)
	}
	defer resp.Body.Close()

	t.Logf("SendWithRetry status: %d", resp.StatusCode)
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestOpenAINormalizeMessages(t *testing.T) {
	p := chatModel(t)
	req := Request{
		Messages: NormalizeMessages([]Message{
			{Role: RoleUser, Content: "check system info"},
		}),
	}
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	t.Logf("Response: %s", resp.Content)
}
