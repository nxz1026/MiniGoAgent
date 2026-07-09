package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

type ToolSchema struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type Message struct {
	Role             Role
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	ToolCallID       string
	Name             string
}

type Request struct {
	Model       string
	Messages    []Message
	Tools       []ToolSchema
	Temperature *float64
	TopP        *float64
	MaxTokens   *int
	Stop        []string
}

type Response struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	Usage            *Usage
	Warning          string
}

type ChunkType int

const (
	ChunkText          ChunkType = iota
	ChunkReasoning
	ChunkToolCallStart
	ChunkToolCall
	ChunkUsage
	ChunkWarn
	ChunkDone
	ChunkError
)

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Chunk struct {
	Type     ChunkType
	Text     string
	ToolCall *ToolCall
	Usage    *Usage
	Warning  string
	Error    string
}

type Protocol interface {
	Chat(ctx context.Context, req Request) (*Response, error)
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

type Config struct {
	APIKey           string
	APIKeys          []string
	BaseURL          string
	Model            string
	StreamTimeout    time.Duration
	RateLimitRPM     int
	RateLimitTPM     int
	ContextWarnPct   int
	ContextCompressPct int
}

type contextKey string

const CtxLogf contextKey = "protocol_logf"

type Factory func(Config) (Protocol, error)

var (
	registry   = map[string]Factory{}
	registryMu sync.RWMutex
)

func Register(kind string, f Factory) {
	registryMu.Lock()
	registry[kind] = f
	registryMu.Unlock()
}

func New(kind string, cfg Config) (Protocol, error) {
	registryMu.RLock()
	f, ok := registry[kind]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider kind: %s", kind)
	}
	return f(cfg)
}
