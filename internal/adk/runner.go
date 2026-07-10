package adk

import (
	"context"
	"fmt"

	adktypes "MiniGoAgent/internal/adk/types"

	"MiniGoAgent/internal/adk/event"
	"MiniGoAgent/internal/adk/llm"
	"MiniGoAgent/internal/adk/middleware"
	"MiniGoAgent/internal/adk/session"
	"MiniGoAgent/internal/adk/tool"
)

type PromptProvider interface {
	SystemPrompt() string
}

type RunnerConfig struct {
	AgentConfig *AgentConfig
	Bridge      *llm.Bridge
	Tools       *tool.ToolRegistry
	Store       session.Store
	Prompt      PromptProvider
	Guardrails  *tool.Guardrails
	Middleware  *middleware.Chain
	EventBus    *event.Bus
}

type Runner struct {
	agent  Agent
	tools  *tool.ToolRegistry
	store  session.Store
	prompt PromptProvider
	guards *tool.Guardrails
	mw     *middleware.Chain
	bus    *event.Bus
}

func NewRunner(cfg *RunnerConfig) (*Runner, error) {
	if cfg == nil {
		return nil, fmt.Errorf("runner config is nil")
	}
	var prompt string
	if cfg.Prompt != nil {
		prompt = cfg.Prompt.SystemPrompt()
	}

	agentCfg := &AgentConfig{
		ModelRef:   cfg.AgentConfig.ModelRef,
		Bridge:     cfg.Bridge,
		Tools:      cfg.Tools,
		ToolNames:  cfg.AgentConfig.ToolNames,
		Prompt:     prompt,
		MaxSteps:   cfg.AgentConfig.MaxSteps,
		Middleware: cfg.Middleware,
		Guardrails: cfg.Guardrails,
	}

	agent, err := NewReactAgent(context.Background(), agentCfg)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	return &Runner{
		agent:  agent,
		tools:  cfg.Tools,
		store:  cfg.Store,
		prompt: cfg.Prompt,
		guards: cfg.Guardrails,
		mw:     cfg.Middleware,
		bus:    cfg.EventBus,
	}, nil
}

func NewRunnerWithAgent(agent Agent, store session.Store, guards *tool.Guardrails, mw *middleware.Chain, bus *event.Bus) *Runner {
	return &Runner{
		agent:  agent,
		store:  store,
		guards: guards,
		mw:     mw,
		bus:    bus,
	}
}

func (r *Runner) Run(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
	if r.guards != nil {
		for _, msg := range req.Messages {
			for _, tc := range msg.ToolCalls {
				result := r.guards.Check(ctx, tc.Name, tc.Arguments)
				if !result.Allowed {
					r.publishEvent(ctx, event.Error, &event.ErrorData{Error: fmt.Errorf("tool %s blocked by guardrails: %s", tc.Name, result.Reason)})
					return nil, fmt.Errorf("tool %s blocked by guardrails: %s", tc.Name, result.Reason)
				}
			}
		}
	}

	if r.store != nil && req.SessionID != "" {
		existing, _ := r.store.Get(ctx, req.SessionID)
		messages := make([]*adktypes.Message, 0, len(existing)+len(req.Messages))
		messages = append(messages, existing...)
		messages = append(messages, req.Messages...)
		req.Messages = messages
	}

	r.publishEvent(ctx, event.AgentStart, &event.AgentStartData{
		SessionID: req.SessionID,
		ToolNames: req.ToolNames,
	})

	var (
		resp *adktypes.Response
		err  error
	)

	if r.mw != nil {
		mwReq := &middleware.ModelRequest{
			Messages:  req.Messages,
			Tools:     req.ToolNames,
			SessionID: req.SessionID,
		}
		mwResp, mwErr := r.mw.AroundModel(ctx, mwReq, func(ctx context.Context, req *middleware.ModelRequest) (*middleware.ModelResponse, error) {
			innerReq := &adktypes.Request{
				Messages:  req.Messages,
				SessionID: req.SessionID,
				ToolNames: req.Tools,
			}
			innerResp, innerErr := r.agent.Run(ctx, innerReq)
			if innerErr != nil {
				return nil, innerErr
			}
			return &middleware.ModelResponse{Messages: innerResp.Messages, Stats: innerResp.Stats}, nil
		})
		if mwErr != nil {
			r.publishEvent(ctx, event.Error, &event.ErrorData{Error: mwErr})
			return nil, mwErr
		}
		resp = &adktypes.Response{Messages: mwResp.Messages, Stats: mwResp.Stats}
	} else {
		resp, err = r.agent.Run(ctx, req)
		if err != nil {
			r.publishEvent(ctx, event.Error, &event.ErrorData{Error: err})
			return nil, err
		}
	}

	if resp == nil {
		resp = &adktypes.Response{}
	}

	if r.store != nil && req.SessionID != "" {
		_ = r.store.Append(ctx, req.SessionID, resp.Messages...)
	}

	r.publishEvent(ctx, event.AgentEnd, &event.AgentEndData{
		SessionID: req.SessionID,
		Stats:     resp.Stats,
	})
	return resp, nil
}

func (r *Runner) Stream(ctx context.Context, req *adktypes.Request) (<-chan adktypes.Event, error) {
	if r.guards != nil {
		for _, msg := range req.Messages {
			for _, tc := range msg.ToolCalls {
				result := r.guards.Check(ctx, tc.Name, tc.Arguments)
				if !result.Allowed {
					errCh := make(chan adktypes.Event, 1)
					errCh <- adktypes.Event{Type: adktypes.EventError, Error: fmt.Errorf("tool %s blocked by guardrails: %s", tc.Name, result.Reason)}
					close(errCh)
					return errCh, nil
				}
			}
		}
	}

	if r.store != nil && req.SessionID != "" {
		existing, _ := r.store.Get(ctx, req.SessionID)
		messages := make([]*adktypes.Message, 0, len(existing)+len(req.Messages))
		messages = append(messages, existing...)
		messages = append(messages, req.Messages...)
		req.Messages = messages
	}

	r.publishEvent(ctx, event.AgentStart, &event.AgentStartData{
		SessionID: req.SessionID,
		ToolNames: req.ToolNames,
	})

	// NOTE: 流式路径仅执行 middleware 的 BeforeModel 钩子。
	// AfterModel 需要完整的模型响应才能运作，而流式响应是增量的、
	// 消费时才逐块产生，无法在此处提供，故不在流式路径执行 AfterModel。
	// 详见 ARCHITECTURE.md「Stream 中间件限制」。
	if r.mw != nil {
		mwReq := &middleware.ModelRequest{
			Messages:  req.Messages,
			Tools:     req.ToolNames,
			SessionID: req.SessionID,
		}
		modified, mwErr := r.mw.BeforeModelChain(ctx, mwReq)
		if mwErr != nil {
			r.publishEvent(ctx, event.Error, &event.ErrorData{Error: mwErr})
			return nil, mwErr
		}
		req.Messages = modified.Messages
		req.ToolNames = modified.Tools
	}

	rawEvents, err := r.agent.Stream(ctx, req)
	if err != nil {
		r.publishEvent(ctx, event.Error, &event.ErrorData{Error: err})
		return nil, err
	}

	events := make(chan adktypes.Event, 64)
	go func() {
		defer close(events)
		defer r.publishEvent(ctx, event.AgentEnd, &event.AgentEndData{
			SessionID: req.SessionID,
		})
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-rawEvents:
				if !ok {
					return
				}
				select {
				case events <- evt:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return events, nil
}

func (r *Runner) Agent() Agent {
	return r.agent
}

func (r *Runner) publishEvent(ctx context.Context, typ event.Type, data any) {
	if r.bus != nil {
		r.bus.Publish(event.Event{Type: typ, Data: data})
	}
}
