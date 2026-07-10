package types

import (
	"testing"
)

func TestRoleConstants(t *testing.T) {
	if RoleSystem != "system" {
		t.Fatalf("RoleSystem expected 'system', got %q", RoleSystem)
	}
	if RoleUser != "user" {
		t.Fatalf("RoleUser expected 'user', got %q", RoleUser)
	}
	if RoleAssistant != "assistant" {
		t.Fatalf("RoleAssistant expected 'assistant', got %q", RoleAssistant)
	}
	if RoleTool != "tool" {
		t.Fatalf("RoleTool expected 'tool', got %q", RoleTool)
	}
}

func TestMessageCreation(t *testing.T) {
	m := &Message{
		Role:             RoleUser,
		Content:          "hello",
		ReasoningContent: "",
		ToolCalls:        nil,
	}
	if m.Role != RoleUser || m.Content != "hello" {
		t.Fatalf("unexpected Message fields: %+v", m)
	}
}

func TestMessageWithToolCalls(t *testing.T) {
	m := &Message{
		Role:    RoleAssistant,
		Content: "calling tool",
		ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Name: "search", Arguments: `{"q":"test"}`},
		},
	}
	if len(m.ToolCalls) != 1 {
		t.Fatalf("expected 1 ToolCall, got %d", len(m.ToolCalls))
	}
	if m.ToolCalls[0].Name != "search" {
		t.Fatalf("expected tool name 'search', got %q", m.ToolCalls[0].Name)
	}
}

func TestToolCallAssignment(t *testing.T) {
	tc := ToolCall{ID: "call_1", Type: "function", Name: "search", Arguments: `{"q":"test"}`}
	if tc.ID != "call_1" || tc.Name != "search" {
		t.Fatalf("unexpected ToolCall fields: %+v", tc)
	}
}

func TestRequestResponse(t *testing.T) {
	req := &Request{
		Messages: []*Message{
			{Role: RoleUser, Content: "hi"},
		},
		SessionID: "sess_1",
		ToolNames: []string{"search", "read"},
	}
	if len(req.Messages) != 1 || req.SessionID != "sess_1" {
		t.Fatalf("unexpected Request fields: %+v", req)
	}

	resp := &Response{
		Messages: []*Message{
			{Role: RoleAssistant, Content: "hello back"},
		},
		Stats: "gpt-4 · 100ms",
	}
	if len(resp.Messages) != 1 || resp.Stats != "gpt-4 · 100ms" {
		t.Fatalf("unexpected Response fields: %+v", resp)
	}
}

func TestEventTypes(t *testing.T) {
	evts := []Event{
		{Type: EventText, Content: "text"},
		{Type: EventReasoning, Content: "thinking"},
		{Type: EventToolCall, ToolID: "call_1"},
		{Type: EventToolResult, Content: "result"},
		{Type: EventError, Error: nil},
		{Type: EventDone, Content: ""},
	}
	if len(evts) != 6 {
		t.Fatalf("expected 6 events, got %d", len(evts))
	}
	if evts[0].Type != EventText || evts[0].Content != "text" {
		t.Fatalf("unexpected EventText: %+v", evts[0])
	}
	if evts[2].ToolID != "call_1" {
		t.Fatalf("unexpected ToolID: %+v", evts[2])
	}
}
