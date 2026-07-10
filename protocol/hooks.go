package protocol

import (
	"context"
	"sync"
)

type LifecycleHook interface {
	Name() string
	BeforeProcess(ctx context.Context, req *Request) (*Request, error)
	AfterProcess(ctx context.Context, req *Request, resp *Response) (*Response, error)
	OnError(ctx context.Context, req *Request, err error) error
}

type LifecycleHookRegistry struct {
	mu    sync.RWMutex
	hooks []LifecycleHook
}

var globalLifecycleHooks = &LifecycleHookRegistry{}

func RegisterLifecycleHook(h LifecycleHook) {
	globalLifecycleHooks.mu.Lock()
	defer globalLifecycleHooks.mu.Unlock()
	globalLifecycleHooks.hooks = append(globalLifecycleHooks.hooks, h)
}

func ClearLifecycleHooks() {
	globalLifecycleHooks.mu.Lock()
	defer globalLifecycleHooks.mu.Unlock()
	globalLifecycleHooks.hooks = nil
}

func RunBeforeProcessHooks(ctx context.Context, req *Request) (*Request, error) {
	globalLifecycleHooks.mu.RLock()
	items := globalLifecycleHooks.hooks
	globalLifecycleHooks.mu.RUnlock()
	var err error
	for _, h := range items {
		req, err = h.BeforeProcess(ctx, req)
		if err != nil {
			return nil, err
		}
	}
	return req, nil
}

func RunAfterProcessHooks(ctx context.Context, req *Request, resp *Response) (*Response, error) {
	globalLifecycleHooks.mu.RLock()
	items := globalLifecycleHooks.hooks
	globalLifecycleHooks.mu.RUnlock()
	var err error
	for _, h := range items {
		resp, err = h.AfterProcess(ctx, req, resp)
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func RunOnErrorHooks(ctx context.Context, req *Request, err error) error {
	globalLifecycleHooks.mu.RLock()
	items := globalLifecycleHooks.hooks
	globalLifecycleHooks.mu.RUnlock()
	for _, h := range items {
		_ = h.OnError(ctx, req, err)
	}
	return err
}

type LifecycleHookFuncs struct {
	name         string
	beforeFn     func(ctx context.Context, req *Request) (*Request, error)
	afterFn      func(ctx context.Context, req *Request, resp *Response) (*Response, error)
	onErrorFn    func(ctx context.Context, req *Request, err error) error
}

func NewLifecycleHook(name string) *LifecycleHookFuncs {
	return &LifecycleHookFuncs{name: name}
}

func (h *LifecycleHookFuncs) Name() string { return h.name }

func (h *LifecycleHookFuncs) BeforeProcess(ctx context.Context, req *Request) (*Request, error) {
	if h.beforeFn != nil {
		return h.beforeFn(ctx, req)
	}
	return req, nil
}

func (h *LifecycleHookFuncs) AfterProcess(ctx context.Context, req *Request, resp *Response) (*Response, error) {
	if h.afterFn != nil {
		return h.afterFn(ctx, req, resp)
	}
	return resp, nil
}

func (h *LifecycleHookFuncs) OnError(ctx context.Context, req *Request, err error) error {
	if h.onErrorFn != nil {
		return h.onErrorFn(ctx, req, err)
	}
	return err
}

func (h *LifecycleHookFuncs) SetBefore(fn func(ctx context.Context, req *Request) (*Request, error)) *LifecycleHookFuncs {
	h.beforeFn = fn
	return h
}

func (h *LifecycleHookFuncs) SetAfter(fn func(ctx context.Context, req *Request, resp *Response) (*Response, error)) *LifecycleHookFuncs {
	h.afterFn = fn
	return h
}

func (h *LifecycleHookFuncs) SetOnError(fn func(ctx context.Context, req *Request, err error) error) *LifecycleHookFuncs {
	h.onErrorFn = fn
	return h
}
