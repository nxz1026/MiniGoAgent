package middleware

import (
	"context"

	adktypes "MiniGoAgent/internal/adk/types"
)

type ModelRequest struct {
	Messages  []*adktypes.Message
	Tools     []string
	SessionID string
}

type ModelResponse struct {
	Messages []*adktypes.Message
	Stats    string
}

type ToolCall struct {
	Name      string
	Arguments string
	ToolID    string
}

type ToolResult struct {
	Name   string
	Result string
	Error  error
	ToolID string
}

type Middleware interface {
	BeforeModel(ctx context.Context, req *ModelRequest) (*ModelRequest, error)
	AfterModel(ctx context.Context, req *ModelRequest, resp *ModelResponse) (*ModelResponse, error)
	BeforeTool(ctx context.Context, call *ToolCall) (*ToolCall, error)
	AfterTool(ctx context.Context, call *ToolCall, result *ToolResult) (*ToolResult, error)
}

type MiddlewareFuncs struct {
	BeforeModelFn func(ctx context.Context, req *ModelRequest) (*ModelRequest, error)
	AfterModelFn  func(ctx context.Context, req *ModelRequest, resp *ModelResponse) (*ModelResponse, error)
	BeforeToolFn  func(ctx context.Context, call *ToolCall) (*ToolCall, error)
	AfterToolFn   func(ctx context.Context, call *ToolCall, result *ToolResult) (*ToolResult, error)
}

func (m MiddlewareFuncs) BeforeModel(ctx context.Context, req *ModelRequest) (*ModelRequest, error) {
	if m.BeforeModelFn == nil {
		return req, nil
	}
	return m.BeforeModelFn(ctx, req)
}

func (m MiddlewareFuncs) AfterModel(ctx context.Context, req *ModelRequest, resp *ModelResponse) (*ModelResponse, error) {
	if m.AfterModelFn == nil {
		return resp, nil
	}
	return m.AfterModelFn(ctx, req, resp)
}

func (m MiddlewareFuncs) BeforeTool(ctx context.Context, call *ToolCall) (*ToolCall, error) {
	if m.BeforeToolFn == nil {
		return call, nil
	}
	return m.BeforeToolFn(ctx, call)
}

func (m MiddlewareFuncs) AfterTool(ctx context.Context, call *ToolCall, result *ToolResult) (*ToolResult, error) {
	if m.AfterToolFn == nil {
		return result, nil
	}
	return m.AfterToolFn(ctx, call, result)
}
