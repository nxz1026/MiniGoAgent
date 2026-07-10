package protocol

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

type MCPRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type MCPResponse struct {
	ID     string      `json:"id"`
	Result any         `json:"result,omitempty"`
	Error  *MCPError   `json:"error,omitempty"`
}

type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type MCPServer struct {
	tracker  *UsageTracker
	upgrader websocket.Upgrader
	mu       sync.RWMutex
}

func NewMCPServer(tracker *UsageTracker) *MCPServer {
	return &MCPServer{
		tracker: tracker,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (s *MCPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("MCP upgrade: %v", err)
		return
	}
	defer conn.Close()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var req MCPRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			s.sendError(conn, "", -32700, "parse error")
			continue
		}
		resp := s.handle(req)
		resp.ID = req.ID
		if err := conn.WriteJSON(resp); err != nil {
			break
		}
	}
}

func (s *MCPServer) handle(req MCPRequest) MCPResponse {
	switch req.Method {
	case "get_stats":
		return s.handleGetStats()
	case "get_daily":
		return s.handleGetDaily(req.Params)
	case "get_models":
		return s.handleGetModels()
	case "get_vendors":
		return s.handleGetVendors()
	case "query_records":
		return s.handleQueryRecords(req.Params)
	default:
		return MCPResponse{Error: &MCPError{Code: -32601, Message: fmt.Sprintf("unknown method: %s", req.Method)}}
	}
}

func (s *MCPServer) handleGetStats() MCPResponse {
	if s.tracker == nil {
		return MCPResponse{Error: &MCPError{Code: -32000, Message: "tracker not available"}}
	}
	stats, err := s.tracker.GetStats()
	if err != nil {
		return MCPResponse{Error: &MCPError{Code: -32000, Message: err.Error()}}
	}
	return MCPResponse{Result: stats}
}

func (s *MCPServer) handleGetDaily(params json.RawMessage) MCPResponse {
	if s.tracker == nil {
		return MCPResponse{Error: &MCPError{Code: -32000, Message: "tracker not available"}}
	}
	var p struct {
		Since string `json:"since"`
		Until string `json:"until"`
	}
	if params != nil {
		json.Unmarshal(params, &p)
	}
	stats, err := s.tracker.GetDailyStats(p.Since, p.Until)
	if err != nil {
		return MCPResponse{Error: &MCPError{Code: -32000, Message: err.Error()}}
	}
	return MCPResponse{Result: stats}
}

func (s *MCPServer) handleGetModels() MCPResponse {
	if s.tracker == nil {
		return MCPResponse{Error: &MCPError{Code: -32000, Message: "tracker not available"}}
	}
	stats, err := s.tracker.GetModelStats()
	if err != nil {
		return MCPResponse{Error: &MCPError{Code: -32000, Message: err.Error()}}
	}
	return MCPResponse{Result: stats}
}

func (s *MCPServer) handleGetVendors() MCPResponse {
	if s.tracker == nil {
		return MCPResponse{Error: &MCPError{Code: -32000, Message: "tracker not available"}}
	}
	stats, err := s.tracker.GetVendorStats()
	if err != nil {
		return MCPResponse{Error: &MCPError{Code: -32000, Message: err.Error()}}
	}
	return MCPResponse{Result: stats}
}

func (s *MCPServer) handleQueryRecords(params json.RawMessage) MCPResponse {
	if s.tracker == nil {
		return MCPResponse{Error: &MCPError{Code: -32000, Message: "tracker not available"}}
	}
	var q UsageQuery
	if params != nil {
		json.Unmarshal(params, &q)
	}
	records, err := s.tracker.Query(q)
	if err != nil {
		return MCPResponse{Error: &MCPError{Code: -32000, Message: err.Error()}}
	}
	return MCPResponse{Result: records}
}

func (s *MCPServer) sendError(conn *websocket.Conn, id string, code int, msg string) {
	conn.WriteJSON(MCPResponse{ID: id, Error: &MCPError{Code: code, Message: msg}})
}
