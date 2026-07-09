package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
	"MiniGoAgent/protocol"
)

func TestGetEnv(t *testing.T) {
	os.Setenv("TEST_KEY", "val")
	if v := getEnv("TEST_KEY", "fallback"); v != "val" {
		t.Fatalf("expected val, got %s", v)
	}
	os.Unsetenv("TEST_KEY")
	if v := getEnv("TEST_KEY", "fallback"); v != "fallback" {
		t.Fatalf("expected fallback, got %s", v)
	}
}

func TestGetEnvInt(t *testing.T) {
	os.Setenv("TEST_INT", "42")
	if v := getEnvInt("TEST_INT", 0); v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
	os.Unsetenv("TEST_INT")
	if v := getEnvInt("TEST_INT", 10); v != 10 {
		t.Fatalf("expected 10, got %d", v)
	}
	os.Setenv("TEST_INT", "invalid")
	if v := getEnvInt("TEST_INT", 5); v != 5 {
		t.Fatalf("expected 5 (fallback on invalid), got %d", v)
	}
	os.Unsetenv("TEST_INT")
}

func TestSplitEnv(t *testing.T) {
	os.Setenv("TEST_LIST", "a,b,c")
	parts := splitEnv("TEST_LIST")
	if len(parts) != 3 || parts[0] != "a" || parts[1] != "b" || parts[2] != "c" {
		t.Fatalf("unexpected: %v", parts)
	}
	os.Unsetenv("TEST_LIST")
	if parts := splitEnv("TEST_LIST"); parts != nil {
		t.Fatalf("expected nil, got %v", parts)
	}
	os.Setenv("TEST_LIST", " a , b , c ")
	parts = splitEnv("TEST_LIST")
	if len(parts) != 3 || parts[0] != "a" || parts[1] != "b" || parts[2] != "c" {
		t.Fatalf("trim failed: %v", parts)
	}
	os.Unsetenv("TEST_LIST")
}

func TestExtractLocalImagePath(t *testing.T) {
	tmp, err := os.MkdirTemp("E:\\", "minigoagent-test-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmp)
	imgPath := filepath.Join(tmp, "test.png")
	os.WriteFile(imgPath, []byte("fake"), 0644)

	if fp := extractLocalImagePath("describe " + imgPath); fp != imgPath {
		t.Fatalf("expected %s, got %s", imgPath, fp)
	}
	if fp := extractLocalImagePath("not a path"); fp != "" {
		t.Fatalf("expected empty, got %s", fp)
	}
}

func TestHistoryDTORoundtrip(t *testing.T) {
	orig := map[string][]*schema.Message{
		"session1": {
			schema.UserMessage("hello"),
			{Role: schema.Assistant, Content: "hi", ToolCalls: []schema.ToolCall{
				{ID: "call_1", Type: "function", Function: schema.FunctionCall{Name: "test_tool", Arguments: `{"x":1}`}},
			}},
			{Role: schema.Tool, ToolCallID: "call_1", Name: "test_tool", Content: `{"result":"ok"}`},
		},
	}
	data, err := marshalSessions(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := unmarshalSessions(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || len(got["session1"]) != 3 {
		t.Fatalf("session count mismatch: %d sessions, %d msgs", len(got), len(got["session1"]))
	}
	if got["session1"][0].Content != "hello" {
		t.Fatalf("content mismatch")
	}
	if len(got["session1"][1].ToolCalls) != 1 {
		t.Fatalf("tool call count mismatch")
	}
	if got["session1"][1].ToolCalls[0].Function.Name != "test_tool" {
		t.Fatalf("tool name mismatch")
	}
	if got["session1"][2].ToolCallID != "call_1" {
		t.Fatalf("tool call id mismatch")
	}
}

func TestHistoryDTOJSONCompat(t *testing.T) {
	jsonStr := `{"s1":[{"role":"user","content":"hi"},{"role":"assistant","content":"","tool_calls":[{"id":"tc1","type":"function","name":"tool1","arguments":"{}"}]}]}`
	sessions, err := unmarshalSessions([]byte(jsonStr))
	if err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if len(sessions["s1"]) != 2 {
		t.Fatalf("expected 2 msgs, got %d", len(sessions["s1"]))
	}
	if sessions["s1"][1].ToolCalls[0].Function.Name != "tool1" {
		t.Fatalf("tool name mismatch")
	}
	// roundtrip
	data, err := marshalSessions(sessions)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	var back map[string][]historyMessage
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("json unmarshal dto: %v", err)
	}
	if back["s1"][1].ToolCalls[0].Name != "tool1" {
		t.Fatalf("dto tool name mismatch")
	}
}

func TestLoadEnv(t *testing.T) {
	os.Clearenv()
	tmp, err := os.MkdirTemp("E:\\", "minigoagent-test-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmp)
	origWd, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(origWd)
	// save original and restore
	orig := ".env"
	os.WriteFile(orig, []byte("TEST_A=hello\nTEST_B=world\n"), 0644)
	defer os.Remove(orig)

	loadEnv()
	if os.Getenv("TEST_A") != "hello" || os.Getenv("TEST_B") != "world" {
		t.Fatalf("env not loaded: TEST_A=%s TEST_B=%s", os.Getenv("TEST_A"), os.Getenv("TEST_B"))
	}
}

type mockProtocol struct{}

func (m *mockProtocol) Chat(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
	return &protocol.Response{Content: "mocked"}, nil
}

func (m *mockProtocol) Stream(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
	return nil, nil
}

func TestForwardChat(t *testing.T) {
	m := &chatModel{proto: &mockProtocol{}, model: "test"}
	resp, err := m.forwardChat(context.Background(), protocol.Request{})
	if err != nil {
		t.Fatalf("forwardChat failed: %v", err)
	}
	if resp.Content != "mocked" {
		t.Fatalf("expected mocked content, got %s", resp.Content)
	}
}
