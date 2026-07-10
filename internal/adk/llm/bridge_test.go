package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"MiniGoAgent/protocol"
)

type mockProtocol struct {
	chatFn   func(ctx context.Context, req protocol.Request) (*protocol.Response, error)
	streamFn func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error)
}

func (m *mockProtocol) Chat(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
	return m.chatFn(ctx, req)
}

func (m *mockProtocol) Stream(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
	return m.streamFn(ctx, req)
}

func TestNewBridge(t *testing.T) {
	proto := &mockProtocol{
		chatFn: func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
			return &protocol.Response{Content: "hello"}, nil
		},
	}
	b := NewBridge(proto, "gpt-4")
	if b == nil {
		t.Fatal("NewBridge returned nil")
	}
	if b.Model() != "gpt-4" {
		t.Fatalf("expected model 'gpt-4', got %q", b.Model())
	}
	if b.Protocol() != proto {
		t.Fatal("Protocol() returned different proto")
	}
}

func TestBridgeGenerate(t *testing.T) {
	b := NewBridge(&mockProtocol{
		chatFn: func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
			if len(req.Messages) != 1 {
				t.Fatalf("expected 1 message, got %d", len(req.Messages))
			}
			if req.Messages[0].Role != protocol.RoleUser {
				t.Fatalf("expected user role, got %v", req.Messages[0].Role)
			}
			return &protocol.Response{Content: "response"}, nil
		},
	}, "gpt-4")

	msg, err := b.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "hello"},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if msg.Content != "response" {
		t.Fatalf("expected 'response', got %q", msg.Content)
	}
}

func TestBridgeGenerateEmptyResponse(t *testing.T) {
	b := NewBridge(&mockProtocol{
		chatFn: func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
			return &protocol.Response{}, nil
		},
	}, "gpt-4")

	_, err := b.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "hello"},
	})
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestBridgeGenerateWithToolCalls(t *testing.T) {
	b := NewBridge(&mockProtocol{
		chatFn: func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
			return &protocol.Response{
				Content: "",
				ToolCalls: []protocol.ToolCall{
					{ID: "call_1", Type: "function", Name: "search", Arguments: `{"q":"test"}`},
				},
			}, nil
		},
	}, "gpt-4")

	msg, err := b.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "search something"},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Function.Name != "search" {
		t.Fatalf("expected function 'search', got %q", msg.ToolCalls[0].Function.Name)
	}
}

func TestBridgeGenerateWithOptions(t *testing.T) {
	b := NewBridge(&mockProtocol{
		chatFn: func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
			if req.MaxTokens == nil || *req.MaxTokens != 100 {
				t.Fatal("expected MaxTokens=100")
			}
			if req.Temperature == nil || *req.Temperature < 0.69 || *req.Temperature > 0.71 {
				t.Fatal("expected Temperature~0.7")
			}
			return &protocol.Response{Content: "ok"}, nil
		},
	}, "gpt-4")

	maxTokens := 100
	temp := float32(0.7)
	_, err := b.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "hi"},
	}, model.WithMaxTokens(maxTokens), model.WithTemperature(temp))
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
}

func TestBridgeStream(t *testing.T) {
	b := NewBridge(&mockProtocol{
		streamFn: func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
			ch := make(chan protocol.Chunk, 2)
			ch <- protocol.Chunk{Type: protocol.ChunkText, Text: "hello "}
			ch <- protocol.Chunk{Type: protocol.ChunkText, Text: "world"}
			close(ch)
			return ch, nil
		},
	}, "gpt-4")

	sr, err := b.Stream(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "say hi"},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	var content string
	for {
		msg, err := sr.Recv()
		if err != nil {
			break
		}
		content += msg.Content
	}
	if content != "hello world" {
		t.Fatalf("expected 'hello world', got %q", content)
	}
}

func TestBridgeStreamReasoning(t *testing.T) {
	b := NewBridge(&mockProtocol{
		streamFn: func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
			ch := make(chan protocol.Chunk, 2)
			ch <- protocol.Chunk{Type: protocol.ChunkReasoning, Text: "thinking..."}
			ch <- protocol.Chunk{Type: protocol.ChunkText, Text: "answer"}
			close(ch)
			return ch, nil
		},
	}, "gpt-4")

	sr, err := b.Stream(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "think"},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	var reasoning, content string
	for {
		msg, err := sr.Recv()
		if err != nil {
			break
		}
		content += msg.Content
		reasoning += msg.ReasoningContent
	}
	if reasoning != "thinking..." {
		t.Fatalf("expected reasoning 'thinking...', got %q", reasoning)
	}
	if content != "answer" {
		t.Fatalf("expected content 'answer', got %q", content)
	}
}

func TestBridgeStreamError(t *testing.T) {
	b := NewBridge(&mockProtocol{
		streamFn: func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
			ch := make(chan protocol.Chunk, 1)
			ch <- protocol.Chunk{Type: protocol.ChunkError, Error: errors.New("stream failed")}
			close(ch)
			return ch, nil
		},
	}, "gpt-4")

	sr, err := b.Stream(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "trigger error"},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	msg, err := sr.Recv()
	if msg != nil {
		t.Fatalf("expected nil message on error, got %+v", msg)
	}
}

func TestBridgeStreamInterruptedRecovery(t *testing.T) {
	b := NewBridge(&mockProtocol{
		streamFn: func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
			ch := make(chan protocol.Chunk, 1)
			ch <- protocol.Chunk{
				Type:  protocol.ChunkError,
				Error: &protocol.StreamInterruptedError{},
			}
			close(ch)
			return ch, nil
		},
		chatFn: func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
			return &protocol.Response{Content: "recovered response"}, nil
		},
	}, "gpt-4")

	sr, err := b.Stream(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "test recovery"},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	var content string
	for {
		msg, err := sr.Recv()
		if err != nil {
			break
		}
		content += msg.Content
	}
	if content != "[流中断，正在恢复...]recovered response" {
		t.Fatalf("expected recovery content, got %q", content)
	}
}

func TestBridgeWithTools(t *testing.T) {
	b := NewBridge(&mockProtocol{
		chatFn: func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
			if len(req.Tools) != 1 {
				t.Fatalf("expected 1 tool, got %d", len(req.Tools))
			}
			if req.Tools[0].Name != "test_tool" {
				t.Fatalf("expected tool 'test_tool', got %q", req.Tools[0].Name)
			}
			return &protocol.Response{Content: "ok"}, nil
		},
	}, "gpt-4")

	withTools, err := b.WithTools([]*schema.ToolInfo{
		{Name: "test_tool", Desc: "a test tool"},
	})
	if err != nil {
		t.Fatalf("WithTools returned error: %v", err)
	}

	_, err = withTools.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "use tool"},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
}

func TestBridgeStatsLine(t *testing.T) {
	b := NewBridge(&mockProtocol{
		chatFn: func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
			return &protocol.Response{Content: "x"}, nil
		},
	}, "gpt-4")

	line := b.StatsLine()
	if line != "gpt-4" {
		t.Fatalf("expected StatsLine 'gpt-4', got %q", line)
	}
}

func TestBridgeStatsJSON(t *testing.T) {
	b := NewBridge(&mockProtocol{
		chatFn: func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
			return &protocol.Response{Content: "x"}, nil
		},
	}, "gpt-4")

	js := b.StatsJSON()
	if js != "" {
		t.Fatalf("expected empty StatsJSON, got %q", js)
	}
}
