package server

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

type AgentRunner interface {
	Generate(ctx context.Context, msgs []*schema.Message) (*schema.Message, error)
	Stream(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error)
}

type PromptProvider interface {
	SystemPrompt() string
}
