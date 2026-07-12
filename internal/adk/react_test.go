package adk

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"MiniGoAgent/internal/adk/llm"
	"MiniGoAgent/internal/adk/middleware"
	"MiniGoAgent/internal/adk/tool"
)

func TestEinoToolsEmptyRegistry(t *testing.T) {
	reg := tool.NewToolRegistry()
	tools := einoTools(reg, []string{"nonexistent"})
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}

func TestEinoToolsNilNames(t *testing.T) {
	reg := tool.NewToolRegistry()
	tools := einoTools(reg, nil)
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools for nil names, got %d", len(tools))
	}
}

func TestNewReactAgentNilBridge(t *testing.T) {
	_, err := NewReactAgent(context.Background(), &AgentConfig{
		Bridge: nil,
	})
	if err == nil {
		t.Fatal("expected error for nil bridge")
	}
}

func TestNewReactAgentNilConfig(t *testing.T) {
	_, err := NewReactAgent(context.Background(), nil)
	if err == nil || err.Error() != "agent config is nil" {
		t.Fatalf("expected nil config error, got %v", err)
	}
}

func TestNewRunnerNilAgentConfig(t *testing.T) {
	_, err := NewRunner(&RunnerConfig{})
	if err == nil || err.Error() != "runner agent config is nil" {
		t.Fatalf("expected nil agent config error, got %v", err)
	}
}

func TestNewReactAgentEmptyToolNames(t *testing.T) {
	reg := tool.NewToolRegistry()
	_, err := NewReactAgent(context.Background(), &AgentConfig{
		Bridge:    llm.NewBridge(nil, "gpt-4"),
		Tools:     reg,
		ToolNames: []string{},
		Prompt:    "you are a test",
		MaxSteps:  1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (bridge=nil proto is ok for NewReactAgent, fails on chat)", err)
	}
}

func TestNewRunnerNilConfig(t *testing.T) {
	_, err := NewRunner(nil)
	if err == nil || err.Error() != "runner config is nil" {
		t.Fatalf("expected 'runner config is nil', got %v", err)
	}
}

func TestNewRunnerWithAgent(t *testing.T) {
	r := NewRunnerWithAgent(nil, nil, nil, nil, nil)
	if r == nil {
		t.Fatal("NewRunnerWithAgent returned nil")
	}
	if r.Agent() != nil {
		t.Fatal("Agent() should return nil")
	}
}

func TestPromptProvider(t *testing.T) {
	pp := &testPromptProvider{prompt: "test prompt"}
	if pp.SystemPrompt() != "test prompt" {
		t.Fatalf("expected 'test prompt', got %q", pp.SystemPrompt())
	}
}

type testPromptProvider struct {
	prompt string
}

func (p *testPromptProvider) SystemPrompt() string {
	return p.prompt
}

type mockInvokableTool struct {
	infoFn func(ctx context.Context) (*schema.ToolInfo, error)
	runFn  func(ctx context.Context, args string, opts ...einotool.Option) (string, error)
}

func (m *mockInvokableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return m.infoFn(ctx)
}

func (m *mockInvokableTool) InvokableRun(ctx context.Context, args string, opts ...einotool.Option) (string, error) {
	return m.runFn(ctx, args, opts...)
}

func TestMiddlewareWrappedToolCallsHooks(t *testing.T) {
	var beforeCalled, afterCalled bool
	chain := middleware.New(middleware.MiddlewareFuncs{
		BeforeToolFn: func(ctx context.Context, call *middleware.ToolCall) (*middleware.ToolCall, error) {
			beforeCalled = true
			return call, nil
		},
		AfterToolFn: func(ctx context.Context, call *middleware.ToolCall, result *middleware.ToolResult) (*middleware.ToolResult, error) {
			afterCalled = true
			return result, nil
		},
	})

	inner := &mockInvokableTool{
		infoFn: func(ctx context.Context) (*schema.ToolInfo, error) {
			return &schema.ToolInfo{Name: "test_tool"}, nil
		},
		runFn: func(ctx context.Context, args string, opts ...einotool.Option) (string, error) {
			return "result", nil
		},
	}

	wrapped := &middlewareWrappedTool{
		name:  "test_tool",
		inner: inner,
		chain: chain,
	}

	info, err := wrapped.Info(context.Background())
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if info.Name != "test_tool" {
		t.Fatalf("expected 'test_tool', got %q", info.Name)
	}

	res, err := wrapped.InvokableRun(context.Background(), `{"x":1}`)
	if err != nil {
		t.Fatalf("InvokableRun returned error: %v", err)
	}
	if res != "result" {
		t.Fatalf("expected 'result', got %q", res)
	}
	if !beforeCalled {
		t.Fatal("BeforeTool not called")
	}
	if !afterCalled {
		t.Fatal("AfterTool not called")
	}
}

func TestMiddlewareWrappedToolBeforeBlocks(t *testing.T) {
	chain := middleware.New(middleware.MiddlewareFuncs{
		BeforeToolFn: func(ctx context.Context, call *middleware.ToolCall) (*middleware.ToolCall, error) {
			return nil, errors.New("tool blocked")
		},
	})

	inner := &mockInvokableTool{
		infoFn: func(ctx context.Context) (*schema.ToolInfo, error) {
			return &schema.ToolInfo{}, nil
		},
		runFn: func(ctx context.Context, args string, opts ...einotool.Option) (string, error) {
			t.Fatal("tool should not be executed")
			return "", nil
		},
	}

	wrapped := &middlewareWrappedTool{
		name:  "blocked_tool",
		inner: inner,
		chain: chain,
	}

	_, err := wrapped.InvokableRun(context.Background(), `{}`)
	if err == nil || err.Error() != "tool blocked" {
		t.Fatalf("expected 'tool blocked', got %v", err)
	}
}

func TestMiddlewareWrappedToolAfterModifiesResult(t *testing.T) {
	chain := middleware.New(middleware.MiddlewareFuncs{
		AfterToolFn: func(ctx context.Context, call *middleware.ToolCall, result *middleware.ToolResult) (*middleware.ToolResult, error) {
			result.Result = "modified:" + result.Result
			return result, nil
		},
	})

	inner := &mockInvokableTool{
		infoFn: func(ctx context.Context) (*schema.ToolInfo, error) {
			return &schema.ToolInfo{}, nil
		},
		runFn: func(ctx context.Context, args string, opts ...einotool.Option) (string, error) {
			return "original", nil
		},
	}

	wrapped := &middlewareWrappedTool{
		name:  "modify_tool",
		inner: inner,
		chain: chain,
	}

	res, err := wrapped.InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("InvokableRun returned error: %v", err)
	}
	if res != "modified:original" {
		t.Fatalf("expected 'modified:original', got %q", res)
	}
}

type mockBaseTool struct {
	infoFn func(ctx context.Context) (*schema.ToolInfo, error)
}

func (m *mockBaseTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return m.infoFn(ctx)
}

func TestMiddlewareWrappedToolNonInvokable(t *testing.T) {
	chain := middleware.New()
	wrapped := &middlewareWrappedTool{
		name:  "base_only",
		inner: &mockBaseTool{infoFn: func(ctx context.Context) (*schema.ToolInfo, error) { return &schema.ToolInfo{}, nil }},
		chain: chain,
	}
	_, err := wrapped.InvokableRun(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected error for non-invokable tool, got nil")
	}
}

func TestGuardrailBlocksRuntimeToolCall(t *testing.T) {
	var executed atomic.Bool
	inner := &mockInvokableTool{
		infoFn: func(ctx context.Context) (*schema.ToolInfo, error) {
			return &schema.ToolInfo{Name: "danger"}, nil
		},
		runFn: func(ctx context.Context, args string, opts ...einotool.Option) (string, error) {
			executed.Store(true)
			return "ran", nil
		},
	}

	guards := tool.NewGuardrails(nil)
	guards.Deny("danger")

	wrapped := &middlewareWrappedTool{
		name:   "danger",
		inner:  inner,
		guards: guards,
	}

	_, err := wrapped.InvokableRun(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected guardrails to block denied tool")
	}
	if executed.Load() {
		t.Fatal("denied tool should not execute")
	}
}

func TestGuardrailAllowsPermittedToolCall(t *testing.T) {
	inner := &mockInvokableTool{
		infoFn: func(ctx context.Context) (*schema.ToolInfo, error) {
			return &schema.ToolInfo{Name: "safe"}, nil
		},
		runFn: func(ctx context.Context, args string, opts ...einotool.Option) (string, error) {
			return "ok", nil
		},
	}

	guards := tool.NewGuardrails(nil)

	wrapped := &middlewareWrappedTool{
		name:   "safe",
		inner:  inner,
		guards: guards,
	}

	res, err := wrapped.InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "ok" {
		t.Fatalf("expected 'ok', got %q", res)
	}
}

func TestEinoToolsWithMiddleware(t *testing.T) {
	reg := tool.NewToolRegistry()
	it := tool.NewFromInvokable(&mockInvokableTool{
		infoFn: func(ctx context.Context) (*schema.ToolInfo, error) {
			return &schema.ToolInfo{Name: "test_mw_tool", Desc: "mw test"}, nil
		},
		runFn: func(ctx context.Context, args string, opts ...einotool.Option) (string, error) {
			return "ok", nil
		},
	})
	reg.Register("test_mw_tool", it)

	chain := middleware.New()
	tools := einoToolsWrapped(reg, []string{"test_mw_tool"}, chain, nil)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if info.Name != "test_mw_tool" {
		t.Fatalf("expected 'test_mw_tool', got %q", info.Name)
	}
}
