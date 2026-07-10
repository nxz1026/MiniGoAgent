package session

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"
)

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
	data, err := Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := Unmarshal(data)
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
	sessions, err := Unmarshal([]byte(jsonStr))
	if err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if len(sessions["s1"]) != 2 {
		t.Fatalf("expected 2 msgs, got %d", len(sessions["s1"]))
	}
	if sessions["s1"][1].ToolCalls[0].Function.Name != "tool1" {
		t.Fatalf("tool name mismatch")
	}
	data, err := Marshal(sessions)
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
