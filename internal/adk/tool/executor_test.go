package tool

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func executorAddFn(_ context.Context, input addInput) (int, error) {
	return input.A + input.B, nil
}

func TestExecutor_Sequential(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "adds", executorAddFn)
	r.Register("add", tool)

	ex := NewToolExecutor(r)
	args, _ := json.Marshal(addInput{A: 1, B: 2})
	calls := []ToolCall{
		{Name: "add", Arguments: args, ToolID: "1"},
		{Name: "add", Arguments: args, ToolID: "2"},
	}

	results := ex.Execute(context.Background(), calls, WithConcurrent(false))
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	for i, res := range results {
		if res.Failed {
			t.Errorf("result %d should not be failed: %v", i, res.Err)
		}
		if res.ToolID != calls[i].ToolID {
			t.Errorf("tool id mismatch")
		}
	}
}

var errFail = errors.New("tool failed")

func failFn(_ context.Context, _ addInput) (int, error) {
	return 0, errFail
}

func TestExecutor_Sequential_FailFast(t *testing.T) {
	r := NewToolRegistry()
	t1, _ := NewFromFn("ok", "ok", executorAddFn)
	r.Register("ok", t1)
	t2, _ := NewFromFn("fail", "fails", failFn)
	r.Register("fail", t2)

	ex := NewToolExecutor(r)
	okArgs, _ := json.Marshal(addInput{A: 1, B: 2})
	calls := []ToolCall{
		{Name: "ok", Arguments: okArgs, ToolID: "1"},
		{Name: "fail", Arguments: nil, ToolID: "2"},
		{Name: "ok", Arguments: okArgs, ToolID: "3"},
	}

	results := ex.Execute(context.Background(), calls, WithConcurrent(false))
	if len(results) != 2 {
		t.Fatalf("want 2 results (fail-fast), got %d", len(results))
	}
	if !results[1].Failed {
		t.Error("second result should be failed")
	}
}

func TestExecutor_Concurrent(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "adds", executorAddFn)
	r.Register("add", tool)

	ex := NewToolExecutor(r)
	args, _ := json.Marshal(addInput{A: 1, B: 2})
	calls := []ToolCall{
		{Name: "add", Arguments: args, ToolID: "1"},
		{Name: "add", Arguments: args, ToolID: "2"},
		{Name: "add", Arguments: args, ToolID: "3"},
	}

	results := ex.Execute(context.Background(), calls)
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	for i, res := range results {
		if res.Failed {
			t.Errorf("result %d should not be failed: %v", i, res.Err)
		}
	}
}

func TestExecutor_ConcurrentError(t *testing.T) {
	r := NewToolRegistry()
	tool, _ := NewFromFn("add", "adds", executorAddFn)
	r.Register("add", tool)
	failTool, _ := NewFromFn("fail", "fails", failFn)
	r.Register("fail", failTool)

	ex := NewToolExecutor(r)
	okArgs, _ := json.Marshal(addInput{A: 1, B: 2})
	calls := []ToolCall{
		{Name: "ok", Arguments: okArgs, ToolID: "1"},
		{Name: "fail", Arguments: nil, ToolID: "2"},
	}

	results := ex.Execute(context.Background(), calls)
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[1].Err == nil {
		t.Error("fail result should have error")
	}
}

func TestExecutor_CancelContext(t *testing.T) {
	r := NewToolRegistry()

	slowFn := func(ctx context.Context, input addInput) (int, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return input.A + input.B, nil
		}
	}

	slowTool, _ := NewFromFn("slow", "slow", slowFn)
	r.Register("slow", slowTool)

	ex := NewToolExecutor(r)
	args, _ := json.Marshal(addInput{A: 1, B: 2})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results := ex.Execute(ctx, []ToolCall{
		{Name: "slow", Arguments: args, ToolID: "1"},
	}, WithConcurrent(false))

	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if !results[0].Failed {
		t.Error("expected failed result after context cancel")
	}
}

func TestExecutor_NotFound(t *testing.T) {
	r := NewToolRegistry()
	ex := NewToolExecutor(r)

	results := ex.Execute(context.Background(), []ToolCall{
		{Name: "nope", Arguments: nil, ToolID: "1"},
	})

	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if !errors.Is(results[0].Err, ErrToolNotFound) {
		t.Errorf("want ErrToolNotFound, got %v", results[0].Err)
	}
}

func TestExecutor_EmptyCalls(t *testing.T) {
	r := NewToolRegistry()
	ex := NewToolExecutor(r)

	results := ex.Execute(context.Background(), nil)
	if len(results) != 0 {
		t.Errorf("want 0 results, got %d", len(results))
	}

	results = ex.Execute(context.Background(), []ToolCall{})
	if len(results) != 0 {
		t.Errorf("want 0 results, got %d", len(results))
	}
}

func TestExecutor_ConcurrencyLimit(t *testing.T) {
	r := NewToolRegistry()
	var active int32
	var maxActive int32
	slow, err := NewFromFn("slow", "slow tool", func(ctx context.Context, in addInput) (int, error) {
		cur := atomic.AddInt32(&active, 1)
		for {
			old := atomic.LoadInt32(&maxActive)
			if cur <= old || atomic.CompareAndSwapInt32(&maxActive, old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		return 0, nil
	})
	if err != nil {
		t.Fatalf("NewFromFn: %v", err)
	}
	r.Register("slow", slow)

	args, _ := json.Marshal(addInput{A: 1, B: 2})
	calls := make([]ToolCall, 50)
	for i := range calls {
		calls[i] = ToolCall{Name: "slow", Arguments: args, ToolID: string(rune(i))}
	}

	ex := NewToolExecutor(r)
	results := ex.Execute(context.Background(), calls)
	if len(results) != 50 {
		t.Fatalf("want 50 results, got %d", len(results))
	}

	limit := int32(maxConcurrency())
	if atomic.LoadInt32(&maxActive) > limit {
		t.Fatalf("concurrency %d exceeded limit %d", maxActive, limit)
	}
}
