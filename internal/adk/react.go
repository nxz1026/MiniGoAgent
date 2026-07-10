package adk

import (
	"context"
	"fmt"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	adktypes "MiniGoAgent/internal/adk/types"

	"MiniGoAgent/internal/adk/convert"
	"MiniGoAgent/internal/adk/llm"
	"MiniGoAgent/internal/adk/middleware"
	"MiniGoAgent/internal/adk/tool"
)

func einoTools(reg *tool.ToolRegistry, names []string) []einotool.BaseTool {
	var out []einotool.BaseTool
	for _, name := range names {
		t := reg.Get(name)
		if t == nil {
			continue
		}
		out = append(out, t.ToEinoTool())
	}
	return out
}

type middlewareWrappedTool struct {
	name   string
	inner  einotool.BaseTool
	chain  *middleware.Chain
	guards *tool.Guardrails
}

func (w *middlewareWrappedTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *middlewareWrappedTool) InvokableRun(ctx context.Context, args string, opts ...einotool.Option) (string, error) {
	inv, ok := w.inner.(einotool.InvokableTool)
	if !ok {
		return "", fmt.Errorf("tool %s is not invokable", w.name)
	}

	if w.guards != nil {
		if res := w.guards.Check(ctx, w.name, args); !res.Allowed {
			return "", fmt.Errorf("tool %s blocked by guardrails: %s", w.name, res.Reason)
		}
	}

	run := func(ctx context.Context, arguments string) (string, error) {
		return inv.InvokableRun(ctx, arguments, opts...)
	}

	if w.chain == nil {
		return run(ctx, args)
	}

	call := &middleware.ToolCall{Name: w.name, Arguments: args}
	result, err := w.chain.AroundTool(ctx, call, func(ctx context.Context, call *middleware.ToolCall) (*middleware.ToolResult, error) {
		res, err := run(ctx, call.Arguments)
		return &middleware.ToolResult{Name: call.Name, Result: res, Error: err}, nil
	})
	if err != nil {
		return "", err
	}
	if result.Error != nil {
		return result.Result, result.Error
	}
	return result.Result, nil
}

func einoToolsWrapped(reg *tool.ToolRegistry, names []string, chain *middleware.Chain, guards *tool.Guardrails) []einotool.BaseTool {
	var out []einotool.BaseTool
	for _, name := range names {
		t := reg.Get(name)
		if t == nil {
			continue
		}
		wrapped := &middlewareWrappedTool{
			name:   name,
			inner:  t.ToEinoTool(),
			chain:  chain,
			guards: guards,
		}
		out = append(out, wrapped)
	}
	return out
}

type ReactAgent struct {
	inner  *react.Agent
	config *AgentConfig
	bridge *llm.Bridge
}

func NewReactAgent(ctx context.Context, cfg *AgentConfig) (*ReactAgent, error) {
	if cfg.Bridge == nil {
		return nil, fmt.Errorf("bridge is required for ReactAgent")
	}
	br := cfg.Bridge

	var tools []einotool.BaseTool
	if cfg.Middleware != nil || cfg.Guardrails != nil {
		tools = einoToolsWrapped(cfg.Tools, cfg.ToolNames, cfg.Middleware, cfg.Guardrails)
	} else {
		tools = einoTools(cfg.Tools, cfg.ToolNames)
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: br,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: tools,
		},
		MaxStep: cfg.MaxSteps,
		MessageRewriter: func(ctx context.Context, msgs []*schema.Message) []*schema.Message {
			if len(msgs) <= 6 {
				return msgs
			}
			return msgs[len(msgs)-6:]
		},
		MessageModifier: func(ctx context.Context, input []*schema.Message) []*schema.Message {
			systemMsg := schema.SystemMessage(cfg.Prompt)
			return append([]*schema.Message{systemMsg}, input...)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create react agent: %w", err)
	}

	return &ReactAgent{
		inner:  agent,
		config: cfg,
		bridge: br,
	}, nil
}

func (a *ReactAgent) Run(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
	msgs := convert.ToEinoSlice(req.Messages)
	result, err := a.inner.Generate(ctx, msgs)
	if err != nil {
		return nil, err
	}

	return &adktypes.Response{
		Messages: convert.FromEinoSlice([]*schema.Message{result}),
		Stats:    a.bridge.StatsLine(),
	}, nil
}

func (a *ReactAgent) Stream(ctx context.Context, req *adktypes.Request) (<-chan adktypes.Event, error) {
	msgs := convert.ToEinoSlice(req.Messages)
	sr, err := a.inner.Stream(ctx, msgs)
	if err != nil {
		return nil, err
	}

	events := make(chan adktypes.Event, 64)
	go func() {
		defer close(events)
		defer sr.Close()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			msg, err := sr.Recv()
			if err != nil {
				return
			}
			if msg.Content != "" {
				select {
				case events <- adktypes.Event{Type: adktypes.EventText, Content: msg.Content}:
				case <-ctx.Done():
					return
				}
			}
			if msg.ReasoningContent != "" {
				select {
				case events <- adktypes.Event{Type: adktypes.EventReasoning, Content: msg.ReasoningContent}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return events, nil
}

var _ Agent = (*ReactAgent)(nil)
