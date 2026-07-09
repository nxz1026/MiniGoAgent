package protocol

import (
	"context"
	"encoding/json"
	"fmt"
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
}

type ChunkType int

const (
	ChunkText          ChunkType = iota
	ChunkReasoning
	ChunkToolCallStart
	ChunkToolCall
	ChunkUsage
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
	Error    string
}

type Protocol interface {
	Chat(ctx context.Context, req Request) (*Response, error)
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

type Config struct {
	APIKey  string
	BaseURL string
	Model   string
}

type Factory func(Config) (Protocol, error)

var registry = map[string]Factory{}

func Register(kind string, f Factory) {
	registry[kind] = f
}

func New(kind string, cfg Config) (Protocol, error) {
	f, ok := registry[kind]
	if !ok {
		return nil, fmt.Errorf("unknown provider kind: %s", kind)
	}
	return f(cfg)
}
