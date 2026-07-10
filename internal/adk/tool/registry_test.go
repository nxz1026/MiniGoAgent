package tool

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func registryAddFn(_ context.Context, input addInput) (int, error) {
	return input.A + input.B, nil
}

func TestRegistry_RegisterGet(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "adds", registryAddFn)
	r.Register("add", tool)

	got := r.Get("add")
	if got == nil {
		t.Fatal("tool not found")
	}
	if got.Name() != "add" {
		t.Errorf("want 'add', got %q", got.Name())
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	r := NewToolRegistry()
	got := r.Get("nonexistent")
	if got != nil {
		t.Error("expected nil for nonexistent tool")
	}
}

func TestRegistry_Names(t *testing.T) {
	r := NewToolRegistry()
	t1, _ := NewFromFn("a", "desc", registryAddFn)
	t2, _ := NewFromFn("b", "desc", registryAddFn)
	r.Register("a", t1)
	r.Register("b", t2)

	names := r.Names()
	if len(names) != 2 {
		t.Errorf("want 2 names, got %d", len(names))
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "desc", registryAddFn)
	r.Register("add", tool)
	r.Unregister("add")

	if r.Get("add") != nil {
		t.Error("tool should be nil after unregister")
	}
}

func TestRegistry_GetDefinitions(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "adds numbers", registryAddFn)
	r.Register("add", tool)

	defs := r.GetDefinitions()
	if len(defs) != 1 {
		t.Fatalf("want 1 definition, got %d", len(defs))
	}
	if defs[0].Name != "add" {
		t.Errorf("want 'add', got %q", defs[0].Name)
	}
}

func TestRegistry_Dispatch(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "adds numbers", registryAddFn)
	r.Register("add", tool)

	args, _ := json.Marshal(addInput{A: 5, B: 3})
	result, err := r.Dispatch(context.Background(), "add", args)
	if err != nil {
		t.Fatal(err)
	}
	var sum int
	if err := json.Unmarshal([]byte(result), &sum); err != nil {
		t.Fatal(err)
	}
	if sum != 8 {
		t.Errorf("want 8, got %d", sum)
	}
}

func TestRegistry_DispatchNotFound(t *testing.T) {
	r := NewToolRegistry()
	_, err := r.Dispatch(context.Background(), "nope", nil)
	if err != ErrToolNotFound {
		t.Errorf("want ErrToolNotFound, got %v", err)
	}
}

func TestRegistry_CheckDefault(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "desc", registryAddFn)
	r.Register("add", tool)

	if !r.Check(context.Background(), "add") {
		t.Error("default check should be true")
	}
}

func TestRegistry_CheckFnTTL(t *testing.T) {
	r := NewToolRegistry()

	var callCount int
	var mu sync.Mutex
	checkFn := func(ctx context.Context) bool {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		return true
	}

	tool, _ := NewFromFn("add", "desc", registryAddFn)
	tool.WithCheck(checkFn)
	r.Register("add", tool)

	r.Check(context.Background(), "add")
	time.Sleep(time.Millisecond)
	r.Check(context.Background(), "add")

	mu.Lock()
	count := callCount
	mu.Unlock()

	if count != 1 {
		t.Errorf("check_fn should be called once (TTL cache), called %d times", count)
	}
}

func TestRegistry_CheckFnTTLExpiry(t *testing.T) {
	r := NewToolRegistry()

	var callCount int
	var mu sync.Mutex
	checkFn := func(ctx context.Context) bool {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		return true
	}

	tool, _ := NewFromFn("add", "desc", registryAddFn)
	tool.WithCheck(checkFn)
	r.Register("add", tool)

	checkFnTTL = time.Millisecond
	defer func() { checkFnTTL = 30 * time.Second }()

	r.Check(context.Background(), "add")
	time.Sleep(5 * time.Millisecond)
	r.Check(context.Background(), "add")

	mu.Lock()
	count := callCount
	mu.Unlock()

	if count < 2 {
		t.Errorf("check_fn should be called again after TTL expiry, called %d times", count)
	}
}

func TestRegistry_CheckFnFailureGrace(t *testing.T) {
	r := NewToolRegistry()

	var returnValue bool
	var mu sync.RWMutex
	checkFn := func(ctx context.Context) bool {
		mu.RLock()
		defer mu.RUnlock()
		return returnValue
	}

	tool, _ := NewFromFn("add", "desc", registryAddFn)
	tool.WithCheck(checkFn)
	r.Register("add", tool)

	mu.Lock()
	returnValue = true
	mu.Unlock()

	r.Check(context.Background(), "add")

	mu.Lock()
	returnValue = false
	mu.Unlock()

	result := r.Check(context.Background(), "add")
	if !result {
		t.Error("check should return true within failure grace window")
	}
}

func TestRegistry_CheckFnFailureAfterGrace(t *testing.T) {
	r := NewToolRegistry()

	var returnValue bool
	var mu sync.RWMutex
	checkFn := func(ctx context.Context) bool {
		mu.RLock()
		defer mu.RUnlock()
		return returnValue
	}

	tool, _ := NewFromFn("add", "desc", registryAddFn)
	tool.WithCheck(checkFn)
	r.Register("add", tool)

	mu.Lock()
	returnValue = true
	mu.Unlock()
	r.Check(context.Background(), "add")

	checkFnFailureGrace = time.Millisecond
	defer func() { checkFnFailureGrace = 60 * time.Second }()

	r.checkCacheMu.Lock()
	r.checkCache["add"] = &checkCacheEntry{available: false, expiresAt: time.Now().Add(-time.Second)}
	r.lastSuccessTime["add"] = time.Now().Add(-2 * time.Millisecond)
	r.checkCacheMu.Unlock()

	mu.Lock()
	returnValue = false
	mu.Unlock()

	result := r.Check(context.Background(), "add")
	if result {
		t.Error("check should return false after failure grace window")
	}

	r.checkCacheMu.RLock()
	cached, ok := r.checkCache["add"]
	r.checkCacheMu.RUnlock()
	if !ok {
		t.Fatal("expected cache entry")
	}
	if cached.available {
		t.Error("cached value should be false")
	}
}

func TestRegistry_InvalidateCache(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "desc", registryAddFn)
	r.Register("add", tool)
	r.Check(context.Background(), "add")
	r.InvalidateCache("add")

	r.checkCacheMu.RLock()
	_, ok := r.checkCache["add"]
	r.checkCacheMu.RUnlock()
	if ok {
		t.Error("cache should be invalidated")
	}
}

func TestRegistry_ClearCache(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "desc", registryAddFn)
	r.Register("add", tool)
	r.Check(context.Background(), "add")
	r.ClearCache()

	r.checkCacheMu.RLock()
	if len(r.checkCache) != 0 {
		t.Error("cache should be empty after clear")
	}
	r.checkCacheMu.RUnlock()
}

func TestRegistry_GetEinoTools(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "desc", registryAddFn)
	r.Register("add", tool)

	einoTools := r.GetEinoTools()
	if len(einoTools) != 1 {
		t.Fatalf("want 1 eino tool, got %d", len(einoTools))
	}
}

func TestRegistry_RegisterCheckFn(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "desc", registryAddFn)
	r.Register("add", tool)

	r.RegisterCheckFn("add", func(ctx context.Context) bool { return false })
	if r.Check(context.Background(), "add") {
		t.Error("check should return false after RegisterCheckFn")
	}
}

func TestRegistry_RegisterCheckFnUnknown(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterCheckFn("nope", func(ctx context.Context) bool { return false })
}

func TestRegistry_CheckFnBlocksByRegistry(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "desc", registryAddFn)
	tool.WithCheck(func(ctx context.Context) bool { return false })
	r.Register("add", tool)

	if r.Check(context.Background(), "add") {
		t.Error("check should return false when check_fn returns false")
	}
}
