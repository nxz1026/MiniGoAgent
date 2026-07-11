package tool

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

var (
	checkFnTTL          = 30 * time.Second
	checkFnFailureGrace = 60 * time.Second
)

type checkCacheEntry struct {
	available bool
	expiresAt time.Time
}

type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*Tool

	checkCacheMu    sync.RWMutex
	checkCache      map[string]*checkCacheEntry
	lastSuccessTime map[string]time.Time
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools:           make(map[string]*Tool),
		checkCache:      make(map[string]*checkCacheEntry),
		lastSuccessTime: make(map[string]time.Time),
	}
}

func (r *ToolRegistry) Register(name string, t *Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[name] = t
}

func (r *ToolRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)

	r.checkCacheMu.Lock()
	delete(r.checkCache, name)
	delete(r.lastSuccessTime, name)
	r.checkCacheMu.Unlock()
}

func (r *ToolRegistry) Get(name string) *Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

func (r *ToolRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	return names
}

func (r *ToolRegistry) GetDefinitions() []*schema.ToolInfo {
	r.mu.RLock()
	tools := make([]*Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	r.mu.RUnlock()

	defs := make([]*schema.ToolInfo, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(context.Background())
		if err != nil {
			continue
		}
		defs = append(defs, info)
	}
	return defs
}

func (r *ToolRegistry) GetEinoTools() []any {
	r.mu.RLock()
	tools := make([]*Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	r.mu.RUnlock()

	einoTools := make([]any, 0, len(tools))
	for _, t := range tools {
		einoTools = append(einoTools, t.ToEinoTool())
	}
	return einoTools
}

func (r *ToolRegistry) Dispatch(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t := r.Get(name)
	if t == nil {
		return "", ErrToolNotFound
	}
	return t.InvokableRun(ctx, string(args))
}

func (r *ToolRegistry) Check(ctx context.Context, name string) bool {
	r.checkCacheMu.RLock()
	cached, ok := r.checkCache[name]
	r.checkCacheMu.RUnlock()
	if ok && time.Now().Before(cached.expiresAt) {
		return cached.available
	}

	r.mu.RLock()
	t := r.tools[name]
	if t == nil {
		r.mu.RUnlock()
		return false
	}
	available := t.Check(ctx)
	r.mu.RUnlock()
	now := time.Now()

	r.checkCacheMu.Lock()
	defer r.checkCacheMu.Unlock()

	if available {
		r.checkCache[name] = &checkCacheEntry{
			available: true,
			expiresAt: now.Add(checkFnTTL),
		}
		r.lastSuccessTime[name] = now
		return true
	}

	lastSuccess, hasLast := r.lastSuccessTime[name]
	if hasLast && now.Sub(lastSuccess) < checkFnFailureGrace {
		return true
	}

	r.checkCache[name] = &checkCacheEntry{
		available: false,
		expiresAt: now.Add(checkFnTTL),
	}
	return false
}

func (r *ToolRegistry) InvalidateCache(name string) {
	r.checkCacheMu.Lock()
	defer r.checkCacheMu.Unlock()
	delete(r.checkCache, name)
}

func (r *ToolRegistry) ClearCache() {
	r.checkCacheMu.Lock()
	defer r.checkCacheMu.Unlock()
	r.checkCache = make(map[string]*checkCacheEntry)
	r.lastSuccessTime = make(map[string]time.Time)
}

func (r *ToolRegistry) RegisterCheckFn(name string, fn CheckFunc) {
	t := r.Get(name)
	if t == nil {
		return
	}
	t.checkFn = fn
}
