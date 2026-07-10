package protocol

import (
	"context"
	"errors"
	"testing"
)

type blockHook struct{}

func (b *blockHook) Name() string { return "block" }
func (b *blockHook) BeforeProcess(ctx context.Context, req *Request) (*Request, error) {
	return nil, errors.New("hook blocked")
}
func (b *blockHook) AfterProcess(ctx context.Context, req *Request, resp *Response) (*Response, error) {
	return resp, nil
}
func (b *blockHook) OnError(ctx context.Context, req *Request, err error) error {
	return err
}

type modifyHook struct {
	beforeCalled bool
	afterCalled  bool
	errCalled    bool
}

func (m *modifyHook) Name() string { return "modify" }
func (m *modifyHook) BeforeProcess(ctx context.Context, req *Request) (*Request, error) {
	m.beforeCalled = true
	req.Model = "modified-" + req.Model
	return req, nil
}
func (m *modifyHook) AfterProcess(ctx context.Context, req *Request, resp *Response) (*Response, error) {
	m.afterCalled = true
	resp.Content = "modified-" + resp.Content
	return resp, nil
}
func (m *modifyHook) OnError(ctx context.Context, req *Request, err error) error {
	m.errCalled = true
	return err
}

func TestLifecycleHook_BlockBefore(t *testing.T) {
	ClearLifecycleHooks()
	defer ClearLifecycleHooks()

	RegisterLifecycleHook(&blockHook{})

	_, err := RunBeforeProcessHooks(context.Background(), &Request{Model: "gpt-4"})
	if err == nil || err.Error() != "hook blocked" {
		t.Fatalf("expected block error, got %v", err)
	}
}

func TestLifecycleHook_BeforeAfter(t *testing.T) {
	ClearLifecycleHooks()
	defer ClearLifecycleHooks()

	m := &modifyHook{}
	RegisterLifecycleHook(m)

	req := &Request{Model: "gpt-4"}
	modifiedReq, err := RunBeforeProcessHooks(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modifiedReq.Model != "modified-gpt-4" {
		t.Fatalf("expected 'modified-gpt-4', got %q", modifiedReq.Model)
	}
	if !m.beforeCalled {
		t.Fatal("before hook not called")
	}

	resp := &Response{Content: "hello"}
	modifiedResp, err := RunAfterProcessHooks(context.Background(), modifiedReq, resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modifiedResp.Content != "modified-hello" {
		t.Fatalf("expected 'modified-hello', got %q", modifiedResp.Content)
	}
	if !m.afterCalled {
		t.Fatal("after hook not called")
	}
}

func TestLifecycleHook_OnError(t *testing.T) {
	ClearLifecycleHooks()
	defer ClearLifecycleHooks()

	m := &modifyHook{}
	RegisterLifecycleHook(m)

	req := &Request{Model: "gpt-4"}
	err := RunOnErrorHooks(context.Background(), req, errors.New("test error"))
	if err == nil || err.Error() != "test error" {
		t.Fatalf("expected original error, got %v", err)
	}
	if !m.errCalled {
		t.Fatal("onError hook not called")
	}
}

func TestLifecycleHook_Chain(t *testing.T) {
	ClearLifecycleHooks()
	defer ClearLifecycleHooks()

	callOrder := []string{}
	h1 := NewLifecycleHook("first").SetBefore(func(ctx context.Context, req *Request) (*Request, error) {
		callOrder = append(callOrder, "first-before")
		req.Model = "h1-" + req.Model
		return req, nil
	}).SetAfter(func(ctx context.Context, req *Request, resp *Response) (*Response, error) {
		callOrder = append(callOrder, "first-after")
		resp.Content = "h1-" + resp.Content
		return resp, nil
	})
	h2 := NewLifecycleHook("second").SetBefore(func(ctx context.Context, req *Request) (*Request, error) {
		callOrder = append(callOrder, "second-before")
		req.Model = "h2-" + req.Model
		return req, nil
	}).SetAfter(func(ctx context.Context, req *Request, resp *Response) (*Response, error) {
		callOrder = append(callOrder, "second-after")
		resp.Content = "h2-" + resp.Content
		return resp, nil
	})
	RegisterLifecycleHook(h1)
	RegisterLifecycleHook(h2)

	req, _ := RunBeforeProcessHooks(context.Background(), &Request{Model: "base"})
	if req.Model != "h2-h1-base" {
		t.Fatalf("expected 'h2-h1-base', got %q", req.Model)
	}

	resp, _ := RunAfterProcessHooks(context.Background(), req, &Response{Content: "out"})
	if resp.Content != "h2-h1-out" {
		t.Fatalf("expected 'h2-h1-out', got %q", resp.Content)
	}

	if len(callOrder) != 4 {
		t.Fatalf("expected 4 calls, got %d: %v", len(callOrder), callOrder)
	}
}
