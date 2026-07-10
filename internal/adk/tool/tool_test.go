package tool

import (
	"context"
	"encoding/json"
	"testing"
)

type addInput struct {
	A int `json:"a" jsonschema:"required"`
	B int `json:"b" jsonschema:"required"`
}

func addFn(_ context.Context, input addInput) (int, error) {
	return input.A + input.B, nil
}

func TestNewFromFn(t *testing.T) {
	tool, err := NewFromFn("add", "adds two numbers", addFn)
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name() != "add" {
		t.Errorf("want 'add', got %q", tool.Name())
	}
}

func TestNewFromFn_Info(t *testing.T) {
	tool, err := NewFromFn("add", "adds two numbers", addFn)
	if err != nil {
		t.Fatal(err)
	}
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "add" {
		t.Errorf("want 'add', got %q", info.Name)
	}
	if info.Desc != "adds two numbers" {
		t.Errorf("want 'adds two numbers', got %q", info.Desc)
	}
}

func TestNewFromFn_Run(t *testing.T) {
	tool, err := NewFromFn("add", "adds two numbers", addFn)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(addInput{A: 3, B: 4})
	result, err := tool.InvokableRun(context.Background(), string(args))
	if err != nil {
		t.Fatal(err)
	}
	var sum int
	if err := json.Unmarshal([]byte(result), &sum); err != nil {
		t.Fatal(err)
	}
	if sum != 7 {
		t.Errorf("want 7, got %d", sum)
	}
}

func TestCheck_Default(t *testing.T) {
	tool, err := NewFromFn("add", "adds two numbers", addFn)
	if err != nil {
		t.Fatal(err)
	}
	if !tool.Check(context.Background()) {
		t.Error("default check should be true")
	}
}

func TestCheck_Custom(t *testing.T) {
	tool, err := NewFromFn("add", "adds two numbers", addFn)
	if err != nil {
		t.Fatal(err)
	}
	tool.WithCheck(func(ctx context.Context) bool { return false })
	if tool.Check(context.Background()) {
		t.Error("custom check should return false")
	}
}

func TestNewFromInvokable(t *testing.T) {
	tool, err := NewFromFn("wrapped", "wrapped tool", addFn)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := NewFromInvokable(tool.invokable)
	if wrapped.Name() != "wrapped" {
		t.Errorf("want 'wrapped', got %q", wrapped.Name())
	}
}
