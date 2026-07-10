package adk

import (
	"context"

	adktypes "MiniGoAgent/internal/adk/types"

	"MiniGoAgent/internal/adk/llm"
	"MiniGoAgent/internal/adk/middleware"
	"MiniGoAgent/internal/adk/tool"
)

type Agent interface {
	Run(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error)
	Stream(ctx context.Context, req *adktypes.Request) (<-chan adktypes.Event, error)
}

type AgentConfig struct {
	ModelRef   llm.ModelRef
	Bridge     *llm.Bridge
	Tools      *tool.ToolRegistry
	ToolNames  []string
	Prompt     string
	MaxSteps   int
	Middleware *middleware.Chain
	Guardrails *tool.Guardrails
}
