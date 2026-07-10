package tools

import (
	"context"
	"errors"
	"testing"
)

type blockInterceptor struct{}

func (b *blockInterceptor) Name() string { return "blocker" }
func (b *blockInterceptor) Before(ctx context.Context, cmd CommandContext) (CommandContext, error) {
	return cmd, errors.New("blocked by interceptor")
}
func (b *blockInterceptor) After(ctx context.Context, cmd CommandContext, result CommandResult) (CommandResult, error) {
	return result, nil
}

type modifyInterceptor struct{}

func (m *modifyInterceptor) Name() string { return "modifier" }
func (m *modifyInterceptor) Before(ctx context.Context, cmd CommandContext) (CommandContext, error) {
	cmd.Command = "echo " + cmd.Command
	return cmd, nil
}
func (m *modifyInterceptor) After(ctx context.Context, cmd CommandContext, result CommandResult) (CommandResult, error) {
	result.Output = "[intercepted] " + result.Output
	return result, nil
}

func TestInterceptorRegistry_Block(t *testing.T) {
	ClearInterceptors()
	defer ClearInterceptors()

	RegisterInterceptor(&blockInterceptor{})

	_, err := RunBeforeInterceptors(context.Background(), CommandContext{Command: "rm -rf /"})
	if err == nil || err.Error() != "blocked by interceptor" {
		t.Fatalf("expected block error, got %v", err)
	}
}

func TestInterceptorRegistry_Modify(t *testing.T) {
	ClearInterceptors()
	defer ClearInterceptors()

	RegisterInterceptor(&modifyInterceptor{})

	cmd, err := RunBeforeInterceptors(context.Background(), CommandContext{Command: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Command != "echo hello" {
		t.Fatalf("expected 'echo hello', got %q", cmd.Command)
	}

	result := RunAfterInterceptors(context.Background(), cmd, CommandResult{Output: "hello world"})
	if result.Output != "[intercepted] hello world" {
		t.Fatalf("expected '[intercepted] hello world', got %q", result.Output)
	}
}

func TestInterceptorRegistry_Chain(t *testing.T) {
	ClearInterceptors()
	defer ClearInterceptors()

	RegisterInterceptor(&modifyInterceptor{})
	RegisterInterceptor(&blockInterceptor{})

	_, err := RunBeforeInterceptors(context.Background(), CommandContext{Command: "test"})
	if err == nil {
		t.Fatal("expected block from second interceptor")
	}
}

func TestInterceptorRegistry_Empty(t *testing.T) {
	ClearInterceptors()

	cmd, err := RunBeforeInterceptors(context.Background(), CommandContext{Command: "ls"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Command != "ls" {
		t.Fatalf("expected 'ls', got %q", cmd.Command)
	}

	result := RunAfterInterceptors(context.Background(), cmd, CommandResult{Output: "file1"})
	if result.Output != "file1" {
		t.Fatalf("expected 'file1', got %q", result.Output)
	}
}
