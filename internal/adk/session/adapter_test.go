package session

import (
	"context"
	"testing"

	adktypes "MiniGoAgent/internal/adk/types"
	appsession "MiniGoAgent/internal/session"
)

func TestNewManagerAdapter(t *testing.T) {
	mgr := appsession.NewManager("test")
	a := NewManagerAdapter(mgr, "sess_1")
	if a == nil {
		t.Fatal("NewManagerAdapter returned nil")
	}
	if a.SessionID() != "sess_1" {
		t.Fatalf("expected SessionID 'sess_1', got %q", a.SessionID())
	}
	if a.Inner() != mgr {
		t.Fatal("Inner() should return the same manager")
	}
}

func TestGetEmptySession(t *testing.T) {
	mgr := appsession.NewManager("test")
	a := NewManagerAdapter(mgr, "empty")
	msgs, err := a.Get(context.Background(), "empty")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if msgs != nil && len(msgs) != 0 {
		t.Fatalf("expected empty messages, got %d", len(msgs))
	}
}

func TestAppendAndSnapshot(t *testing.T) {
	mgr := appsession.NewManager("test")
	a := NewManagerAdapter(mgr, "sess_a")

	msgs := []*adktypes.Message{
		{Role: adktypes.RoleUser, Content: "hello"},
		{Role: adktypes.RoleAssistant, Content: "hi"},
	}
	err := a.Append(context.Background(), "sess_a", msgs...)
	if err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	snap, err := a.Snapshot(context.Background(), "sess_a")
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if len(snap) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(snap))
	}
	if snap[0].Content != "hello" || snap[1].Content != "hi" {
		t.Fatalf("unexpected content: %q, %q", snap[0].Content, snap[1].Content)
	}
}

func TestAppendWithToolCalls(t *testing.T) {
	mgr := appsession.NewManager("test")
	a := NewManagerAdapter(mgr, "sess_b")

	msgs := []*adktypes.Message{
		{
			Role:    adktypes.RoleAssistant,
			Content: "searching",
			ToolCalls: []adktypes.ToolCall{
				{ID: "call_1", Type: "function", Name: "search", Arguments: `{"q":"test"}`},
			},
		},
		{
			Role:       adktypes.RoleTool,
			Content:    `{"result":"ok"}`,
			ToolCallID: "call_1",
		},
	}
	err := a.Append(context.Background(), "sess_b", msgs...)
	if err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	snap, err := a.Snapshot(context.Background(), "sess_b")
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if len(snap) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(snap))
	}
	if len(snap[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(snap[0].ToolCalls))
	}
	if snap[0].ToolCalls[0].Name != "search" {
		t.Fatalf("expected tool name 'search', got %q", snap[0].ToolCalls[0].Name)
	}
	if snap[1].ToolCallID != "call_1" {
		t.Fatalf("expected ToolCallID 'call_1', got %q", snap[1].ToolCallID)
	}
}

func TestMultipleSessions(t *testing.T) {
	mgr := appsession.NewManager("test")
	a1 := NewManagerAdapter(mgr, "sess_1")
	a2 := NewManagerAdapter(mgr, "sess_2")

	a1.Append(context.Background(), "sess_1", &adktypes.Message{Role: adktypes.RoleUser, Content: "msg1"})
	a2.Append(context.Background(), "sess_2", &adktypes.Message{Role: adktypes.RoleUser, Content: "msg2"})

	snap1, _ := a1.Snapshot(context.Background(), "sess_1")
	snap2, _ := a2.Snapshot(context.Background(), "sess_2")

	if len(snap1) != 1 || snap1[0].Content != "msg1" {
		t.Fatalf("session 1 unexpected: %+v", snap1)
	}
	if len(snap2) != 1 || snap2[0].Content != "msg2" {
		t.Fatalf("session 2 unexpected: %+v", snap2)
	}
}

func TestToAdkMessagesEdgeCases(t *testing.T) {
	mgr := appsession.NewManager("test")
	a := NewManagerAdapter(mgr, "edge")

	err := a.Append(context.Background(), "edge")
	if err != nil {
		t.Fatalf("Append with no messages returned error: %v", err)
	}

	snap, err := a.Snapshot(context.Background(), "edge")
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if len(snap) != 0 {
		t.Fatalf("expected 0 messages for empty append, got %d", len(snap))
	}
}

func TestAdkToEinoRoundtrip(t *testing.T) {
	mgr := appsession.NewManager("test")
	a := NewManagerAdapter(mgr, "rt")

	orig := []*adktypes.Message{
		{Role: adktypes.RoleUser, Content: "hello"},
		{Role: adktypes.RoleAssistant, Content: "world"},
	}

	err := a.Append(context.Background(), "rt", orig...)
	if err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	snap, err := a.Snapshot(context.Background(), "rt")
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}

	if len(snap) != len(orig) {
		t.Fatalf("length mismatch: %d vs %d", len(snap), len(orig))
	}
	for i := range orig {
		if snap[i].Role != orig[i].Role || snap[i].Content != orig[i].Content {
			t.Fatalf("message %d mismatch: got %+v, want %+v", i, snap[i], orig[i])
		}
	}
}
