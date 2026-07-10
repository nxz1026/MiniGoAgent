package middleware

import (
	"context"
	"errors"
	"testing"

	adktypes "MiniGoAgent/internal/adk/types"
)

type recorder struct {
	events []string
}

func TestChain_AroundModel(t *testing.T) {
	var r recorder

	mw1 := MiddlewareFuncs{
		BeforeModelFn: func(ctx context.Context, req *ModelRequest) (*ModelRequest, error) {
			r.events = append(r.events, "m1_before")
			return req, nil
		},
		AfterModelFn: func(ctx context.Context, req *ModelRequest, resp *ModelResponse) (*ModelResponse, error) {
			r.events = append(r.events, "m1_after")
			return resp, nil
		},
	}

	mw2 := MiddlewareFuncs{
		BeforeModelFn: func(ctx context.Context, req *ModelRequest) (*ModelRequest, error) {
			r.events = append(r.events, "m2_before")
			return req, nil
		},
		AfterModelFn: func(ctx context.Context, req *ModelRequest, resp *ModelResponse) (*ModelResponse, error) {
			r.events = append(r.events, "m2_after")
			return resp, nil
		},
	}

	chain := New(mw1, mw2)

	resp, err := chain.AroundModel(context.Background(), &ModelRequest{}, func(ctx context.Context, req *ModelRequest) (*ModelResponse, error) {
		r.events = append(r.events, "model")
		return &ModelResponse{}, nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("resp is nil")
	}

	expected := []string{"m1_before", "m2_before", "model", "m2_after", "m1_after"}
	if len(r.events) != len(expected) {
		t.Fatalf("want %v, got %v", expected, r.events)
	}
	for i, e := range expected {
		if r.events[i] != e {
			t.Errorf("step %d: want %s, got %s", i, e, r.events[i])
		}
	}
}

func TestChain_AroundTool(t *testing.T) {
	var r recorder

	mw := MiddlewareFuncs{
		BeforeToolFn: func(ctx context.Context, call *ToolCall) (*ToolCall, error) {
			r.events = append(r.events, "before_tool")
			return call, nil
		},
		AfterToolFn: func(ctx context.Context, call *ToolCall, result *ToolResult) (*ToolResult, error) {
			r.events = append(r.events, "after_tool")
			return result, nil
		},
	}

	chain := New(mw)

	result, err := chain.AroundTool(context.Background(), &ToolCall{Name: "test"}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		r.events = append(r.events, "tool")
		return &ToolResult{Name: "test", Result: "ok"}, nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.Result != "ok" {
		t.Errorf("want 'ok', got %q", result.Result)
	}

	expected := []string{"before_tool", "tool", "after_tool"}
	if len(r.events) != len(expected) {
		t.Fatalf("want %v, got %v", expected, r.events)
	}
	for i, e := range expected {
		if r.events[i] != e {
			t.Errorf("step %d: want %s, got %s", i, e, r.events[i])
		}
	}
}

func TestChain_BeforeModelError(t *testing.T) {
	mw := MiddlewareFuncs{
		BeforeModelFn: func(ctx context.Context, req *ModelRequest) (*ModelRequest, error) {
			return nil, errors.New("blocked")
		},
	}

	chain := New(mw)
	_, err := chain.AroundModel(context.Background(), &ModelRequest{}, func(ctx context.Context, req *ModelRequest) (*ModelResponse, error) {
		return &ModelResponse{}, nil
	})

	if err == nil || err.Error() != "blocked" {
		t.Errorf("want 'blocked', got %v", err)
	}
}

func TestChain_BeforeToolError(t *testing.T) {
	mw := MiddlewareFuncs{
		BeforeToolFn: func(ctx context.Context, call *ToolCall) (*ToolCall, error) {
			return nil, errors.New("denied")
		},
	}

	chain := New(mw)
	_, err := chain.AroundTool(context.Background(), &ToolCall{Name: "test"}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		return &ToolResult{Name: "test", Result: "ok"}, nil
	})

	if err == nil || err.Error() != "denied" {
		t.Errorf("want 'denied', got %v", err)
	}
}

func TestChain_Empty(t *testing.T) {
	chain := New()

	called := false
	_, err := chain.AroundModel(context.Background(), &ModelRequest{}, func(ctx context.Context, req *ModelRequest) (*ModelResponse, error) {
		called = true
		return &ModelResponse{}, nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("next should be called")
	}

	called = false
	_, err = chain.AroundTool(context.Background(), &ToolCall{}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{}, nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("next should be called")
	}
}

func TestChain_MiddlewareFuncsPartial(t *testing.T) {
	mw := MiddlewareFuncs{
		BeforeModelFn: func(ctx context.Context, req *ModelRequest) (*ModelRequest, error) {
			return req, nil
		},
	}

	chain := New(mw)
	resp, err := chain.AroundModel(context.Background(), &ModelRequest{}, func(ctx context.Context, req *ModelRequest) (*ModelResponse, error) {
		return &ModelResponse{Stats: "ok"}, nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if resp.Stats != "ok" {
		t.Errorf("want 'ok', got %q", resp.Stats)
	}
}

func TestChain_AfterModelModifiesResponse(t *testing.T) {
	mw := MiddlewareFuncs{
		AfterModelFn: func(ctx context.Context, req *ModelRequest, resp *ModelResponse) (*ModelResponse, error) {
			resp.Stats = "modified"
			return resp, nil
		},
	}

	chain := New(mw)
	resp, err := chain.AroundModel(context.Background(), &ModelRequest{}, func(ctx context.Context, req *ModelRequest) (*ModelResponse, error) {
		return &ModelResponse{Stats: "original"}, nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if resp.Stats != "modified" {
		t.Errorf("want 'modified', got %q", resp.Stats)
	}
}

func TestMiddlewareFuncs_DefaultNoop(t *testing.T) {
	mw := MiddlewareFuncs{}

	req, err := mw.BeforeModel(context.Background(), &ModelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if req == nil {
		t.Fatal("req should not be nil")
	}

	resp, err := mw.AfterModel(context.Background(), &ModelRequest{}, &ModelResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("resp should not be nil")
	}

	call, err := mw.BeforeTool(context.Background(), &ToolCall{})
	if err != nil {
		t.Fatal(err)
	}
	if call == nil {
		t.Fatal("call should not be nil")
	}

	result, err := mw.AfterTool(context.Background(), &ToolCall{}, &ToolResult{})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
}

var _ = adktypes.EventType(0) // ensure import is used
