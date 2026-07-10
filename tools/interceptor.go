package tools

import (
	"context"
	"sync"
)

type CommandContext struct {
	Command   string
	SessionID string
}

type CommandResult struct {
	Output   string
	Err      error
}

type Interceptor interface {
	Name() string
	Before(ctx context.Context, cmd CommandContext) (CommandContext, error)
	After(ctx context.Context, cmd CommandContext, result CommandResult) (CommandResult, error)
}

type InterceptorRegistry struct {
	mu    sync.RWMutex
	items []Interceptor
}

var globalRegistry = &InterceptorRegistry{}

func RegisterInterceptor(i Interceptor) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.items = append(globalRegistry.items, i)
}

func ClearInterceptors() {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.items = nil
}

func RunBeforeInterceptors(ctx context.Context, cmd CommandContext) (CommandContext, error) {
	globalRegistry.mu.RLock()
	items := globalRegistry.items
	globalRegistry.mu.RUnlock()
	var err error
	for _, i := range items {
		cmd, err = i.Before(ctx, cmd)
		if err != nil {
			return cmd, err
		}
	}
	return cmd, nil
}

func RunAfterInterceptors(ctx context.Context, cmd CommandContext, result CommandResult) CommandResult {
	globalRegistry.mu.RLock()
	items := globalRegistry.items
	globalRegistry.mu.RUnlock()
	var err error
	for _, i := range items {
		result, err = i.After(ctx, cmd, result)
		if err != nil {
			result.Err = err
			return result
		}
	}
	return result
}
