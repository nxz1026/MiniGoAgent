package protocol

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestMCPServer_GetStats(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mcp_test.db")
	ut, err := NewUsageTracker(dbPath)
	if err != nil {
		t.Fatalf("NewUsageTracker: %v", err)
	}
	defer ut.Close()

	ut.Record("s1", "gpt-4", "openai", 5.0, 1000, 200)
	ut.Record("s2", "claude-3", "anthropic", 10.0, 2000, 500)

	srv := NewMCPServer(ut)
	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/mcp"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req, _ := json.Marshal(MCPRequest{ID: "1", Method: "get_stats"})
	if err := conn.WriteMessage(websocket.TextMessage, req); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, resp, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var mcpResp MCPResponse
	if err := json.Unmarshal(resp, &mcpResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if mcpResp.Error != nil {
		t.Fatalf("unexpected error: %v", mcpResp.Error.Message)
	}
	if mcpResp.ID != "1" {
		t.Fatalf("expected id '1', got %q", mcpResp.ID)
	}
	resultMap, ok := mcpResp.Result.(map[string]any)
	if !ok {
		data, _ := json.Marshal(mcpResp.Result)
		json.Unmarshal(data, &resultMap)
	}
	totalCalls := int(resultMap["total_calls"].(float64))
	if totalCalls != 2 {
		t.Fatalf("expected 2 total calls, got %d", totalCalls)
	}
}

func TestMCPServer_UnknownMethod(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mcp_unknown.db")
	ut, _ := NewUsageTracker(dbPath)
	defer ut.Close()

	srv := NewMCPServer(ut)
	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/mcp"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req, _ := json.Marshal(MCPRequest{ID: "2", Method: "nonexistent"})
	conn.WriteMessage(websocket.TextMessage, req)

	_, resp, _ := conn.ReadMessage()
	var m MCPResponse
	json.Unmarshal(resp, &m)
	if m.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if m.Error.Code != -32601 {
		t.Fatalf("expected code -32601, got %d", m.Error.Code)
	}
}

func TestMCPServer_QueryRecords(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mcp_query.db")
	ut, _ := NewUsageTracker(dbPath)
	defer ut.Close()

	ut.Record("sess-a", "gpt-4", "openai", 3.0, 500, 50)
	ut.Record("sess-b", "claude-3", "anthropic", 8.0, 1500, 300)

	srv := NewMCPServer(ut)
	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/mcp"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	params, _ := json.Marshal(map[string]string{"session_id": "sess-a"})
	req, _ := json.Marshal(MCPRequest{ID: "3", Method: "query_records", Params: params})
	conn.WriteMessage(websocket.TextMessage, req)

	_, resp, _ := conn.ReadMessage()
	var m MCPResponse
	json.Unmarshal(resp, &m)
	if m.Error != nil {
		t.Fatalf("unexpected error: %v", m.Error.Message)
	}
	raw, _ := json.Marshal(m.Result)
	var records []UsageRecord
	json.Unmarshal(raw, &records)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].SessionID != "sess-a" {
		t.Fatalf("expected session 'sess-a', got %q", records[0].SessionID)
	}
}
