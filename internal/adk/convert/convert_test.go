package convert

import (
	"testing"

	"github.com/cloudwego/eino/schema"

	adktypes "MiniGoAgent/internal/adk/types"
)

func TestRoundtripBasic(t *testing.T) {
	eino := []*schema.Message{
		{Role: schema.User, Content: "hello"},
		{Role: schema.Assistant, Content: "world", ReasoningContent: "thinking"},
	}

	adk := FromEinoSlice(eino)
	if len(adk) != 2 {
		t.Fatalf("expected 2 adk messages, got %d", len(adk))
	}
	if adk[0].Role != adktypes.RoleUser || adk[0].Content != "hello" {
		t.Fatalf("first message mismatch: %+v", adk[0])
	}
	if adk[1].ReasoningContent != "thinking" {
		t.Fatalf("reasoning mismatch: %q", adk[1].ReasoningContent)
	}

	back := ToEinoSlice(adk)
	if len(back) != 2 {
		t.Fatalf("expected 2 eino messages, got %d", len(back))
	}
	if back[0].Role != schema.User || back[0].Content != "hello" {
		t.Fatalf("roundtrip first message mismatch: %+v", back[0])
	}
}

func TestRoundtripToolCalls(t *testing.T) {
	eino := []*schema.Message{
		{
			Role:    schema.Assistant,
			Content: "calling tool",
			ToolCalls: []schema.ToolCall{
				{
					ID: "call_1", Type: "function",
					Function: schema.FunctionCall{Name: "search", Arguments: `{"q":"test"}`},
				},
			},
		},
		{
			Role:       schema.Tool,
			Content:    `{"result":"ok"}`,
			ToolCallID: "call_1",
			Name:       "search",
		},
	}

	adk := FromEinoSlice(eino)
	if len(adk[0].ToolCalls) != 1 || adk[0].ToolCalls[0].Name != "search" {
		t.Fatalf("tool call mismatch: %+v", adk[0].ToolCalls)
	}
	if adk[1].ToolCallID != "call_1" {
		t.Fatalf("expected ToolCallID 'call_1', got %q", adk[1].ToolCallID)
	}

	back := ToEinoSlice(adk)
	if back[1].ToolCallID != "call_1" {
		t.Fatalf("roundtrip ToolCallID mismatch: %q", back[1].ToolCallID)
	}
	if len(back[0].ToolCalls) != 1 || back[0].ToolCalls[0].Function.Name != "search" {
		t.Fatalf("roundtrip tool call mismatch: %+v", back[0].ToolCalls)
	}
}

func TestNilHandling(t *testing.T) {
	if ToEino(nil) != nil {
		t.Fatal("ToEino(nil) should return nil")
	}
	if FromEino(nil) != nil {
		t.Fatal("FromEino(nil) should return nil")
	}
}

func TestEmptyMessage(t *testing.T) {
	if msg := ToEino(&adktypes.Message{}); msg == nil {
		t.Fatal("ToEino returned nil for empty message")
	}
	if msg := FromEino(&schema.Message{}); msg == nil {
		t.Fatal("FromEino returned nil for empty message")
	}
}
