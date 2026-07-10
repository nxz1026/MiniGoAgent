package tool

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
)

func maxConcurrency() int {
	if n := runtime.NumCPU(); n > 4 {
		return n
	}
	return 4
}

type ToolCall struct {
	Name      string
	Arguments json.RawMessage
	ToolID    string
}

type ToolResult struct {
	Name   string
	Result string
	Err    error
	ToolID string
	Failed bool
}

type ExecuteOption func(*executeConfig)

type executeConfig struct {
	concurrent bool
}

func WithConcurrent(v bool) ExecuteOption {
	return func(c *executeConfig) {
		c.concurrent = v
	}
}

type ToolExecutor struct {
	registry *ToolRegistry
}

func NewToolExecutor(registry *ToolRegistry) *ToolExecutor {
	return &ToolExecutor{registry: registry}
}

func (e *ToolExecutor) Execute(ctx context.Context, calls []ToolCall, opts ...ExecuteOption) []ToolResult {
	cfg := &executeConfig{concurrent: true}
	for _, o := range opts {
		o(cfg)
	}

	if cfg.concurrent && len(calls) > 1 {
		return e.executeConcurrent(ctx, calls)
	}
	return e.executeSequential(ctx, calls)
}

func (e *ToolExecutor) executeSequential(ctx context.Context, calls []ToolCall) []ToolResult {
	results := make([]ToolResult, 0, len(calls))
	for _, call := range calls {
		select {
		case <-ctx.Done():
			results = append(results, ToolResult{
				Name: call.Name, ToolID: call.ToolID, Failed: true,
				Err: ctx.Err(),
			})
			return results
		default:
		}

		result := e.runOne(ctx, call)
		results = append(results, result)
		if result.Failed {
			return results
		}
	}
	return results
}

func (e *ToolExecutor) executeConcurrent(ctx context.Context, calls []ToolCall) []ToolResult {
	results := make([]ToolResult, len(calls))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrency())

	for i, call := range calls {
		wg.Add(1)
		i, call := i, call
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = e.runOne(ctx, call)
		}()
	}

	wg.Wait()
	return results
}

func (e *ToolExecutor) runOne(ctx context.Context, call ToolCall) ToolResult {
	t := e.registry.Get(call.Name)
	if t == nil {
		return ToolResult{Name: call.Name, ToolID: call.ToolID, Failed: true, Err: ErrToolNotFound}
	}

	result, err := t.InvokableRun(ctx, string(call.Arguments))
	if err != nil {
		return ToolResult{Name: call.Name, ToolID: call.ToolID, Failed: true, Err: err}
	}
	return ToolResult{Name: call.Name, ToolID: call.ToolID, Result: result}
}
