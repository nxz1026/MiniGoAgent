package middleware

import (
	"context"
)

type Chain struct {
	middlewares []Middleware
}

func New(mws ...Middleware) *Chain {
	return &Chain{middlewares: mws}
}

func (c *Chain) Use(mw Middleware) {
	c.middlewares = append(c.middlewares, mw)
}

func (c *Chain) AroundModel(ctx context.Context, req *ModelRequest, next func(ctx context.Context, req *ModelRequest) (*ModelResponse, error)) (*ModelResponse, error) {
	var err error
	for _, mw := range c.middlewares {
		req, err = mw.BeforeModel(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	resp, err := next(ctx, req)
	if err != nil {
		return nil, err
	}

	for i := len(c.middlewares) - 1; i >= 0; i-- {
		resp, err = c.middlewares[i].AfterModel(ctx, req, resp)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// BeforeModelChain 仅按顺序执行所有中间件的 BeforeModel 钩子，返回改写后的请求。
// 用于无法承载完整 onion 语义的流式路径（AfterModel 不适用）。
func (c *Chain) BeforeModelChain(ctx context.Context, req *ModelRequest) (*ModelRequest, error) {
	var err error
	for _, mw := range c.middlewares {
		req, err = mw.BeforeModel(ctx, req)
		if err != nil {
			return nil, err
		}
	}
	return req, nil
}

func (c *Chain) AroundTool(ctx context.Context, call *ToolCall, next func(ctx context.Context, call *ToolCall) (*ToolResult, error)) (*ToolResult, error) {
	var err error
	for _, mw := range c.middlewares {
		call, err = mw.BeforeTool(ctx, call)
		if err != nil {
			return nil, err
		}
	}

	result, err := next(ctx, call)
	if err != nil {
		return nil, err
	}

	for i := len(c.middlewares) - 1; i >= 0; i-- {
		result, err = c.middlewares[i].AfterTool(ctx, call, result)
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}
