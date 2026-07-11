package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"MiniGoAgent/internal/adk"
	"MiniGoAgent/internal/adk/convert"
	"MiniGoAgent/internal/adk/event"
	"MiniGoAgent/internal/adk/llm"
	adktool "MiniGoAgent/internal/adk/tool"
	adktypes "MiniGoAgent/internal/adk/types"
	appsession "MiniGoAgent/internal/session"
	"MiniGoAgent/protocol"
)

type mockProtoE2E struct {
	mu       sync.Mutex
	chatFn   func(ctx context.Context, req protocol.Request) (*protocol.Response, error)
	streamFn func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error)
	calls    int
}

func (m *mockProtoE2E) Chat(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return m.chatFn(ctx, req)
}

func (m *mockProtoE2E) Stream(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return m.streamFn(ctx, req)
}

type testAgentAdapter struct {
	runner *adk.Runner
}

func (a *testAgentAdapter) Generate(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
	adkMsgs := convert.FromEinoSlice(msgs)
	resp, err := a.runner.Run(ctx, &adktypes.Request{Messages: adkMsgs})
	if err != nil {
		return nil, err
	}
	if len(resp.Messages) == 0 {
		return &schema.Message{Role: schema.Assistant, Content: ""}, nil
	}
	return convert.ToEino(resp.Messages[len(resp.Messages)-1]), nil
}

func (a *testAgentAdapter) Stream(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	adkMsgs := convert.FromEinoSlice(msgs)
	events, err := a.runner.Stream(ctx, &adktypes.Request{Messages: adkMsgs})
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](64)
	go func() {
		defer sw.Close()
		for evt := range events {
			switch evt.Type {
			case adktypes.EventText:
				sw.Send(&schema.Message{Role: schema.Assistant, Content: evt.Content}, nil)
			case adktypes.EventError:
				return
			}
		}
	}()
	return sr, nil
}

type simplePromptProvider struct{}

func (p *simplePromptProvider) SystemPrompt() string {
	return "you are a test assistant"
}

func setupTestServer(t *testing.T, chatFn func(ctx context.Context, req protocol.Request) (*protocol.Response, error)) (*httptest.Server, *mockProtoE2E, *event.Bus) {
	t.Helper()

	mockProto := &mockProtoE2E{
		chatFn: chatFn,
		streamFn: func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
			ch := make(chan protocol.Chunk, 2)
			ch <- protocol.Chunk{Type: protocol.ChunkText, Text: "hello "}
			ch <- protocol.Chunk{Type: protocol.ChunkText, Text: "world"}
			close(ch)
			return ch, nil
		},
	}

	br := llm.NewBridge(mockProto, "test-model")
	reg := adktool.NewToolRegistry()
	toolNames := []string{}

	agent, err := adk.NewReactAgent(context.Background(), &adk.AgentConfig{
		Bridge:    br,
		Tools:     reg,
		ToolNames: toolNames,
		Prompt:    "you are a test assistant",
		MaxSteps:  1,
	})
	if err != nil {
		t.Fatalf("NewReactAgent: %v", err)
	}

	bus := event.NewBus()
	runner := adk.NewRunnerWithAgent(agent, nil, nil, nil, bus)

	adapter := &testAgentAdapter{runner: runner}
	mgr := appsession.NewManager("test")

	srv := New(adapter, br, mgr, []byte("<html>test</html>"), &simplePromptProvider{})
	srv.LoadHistory()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			srv.ServeFrontend(w, r)
		case "/api/chat":
			srv.HandleChat(w, r)
		case "/api/chat/stream":
			srv.HandleChatStream(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, mockProto, bus
}

func TestIntegration_ChatEndpoint(t *testing.T) {
	ts, mockProto, _ := setupTestServer(t, func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
		if len(req.Messages) == 0 {
			return nil, io.ErrUnexpectedEOF
		}
		return &protocol.Response{
			Content:          "test response",
			ReasoningContent: "thinking...",
		}, nil
	})
	mockProto.streamFn = func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
		ch := make(chan protocol.Chunk, 1)
		ch <- protocol.Chunk{Type: protocol.ChunkText, Text: "test response"}
		close(ch)
		return ch, nil
	}

	resp, err := http.Post(ts.URL+"/api/chat", "application/json",
		strings.NewReader(`{"message":"hello"}`))
	if err != nil {
		t.Fatalf("POST /api/chat: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	reply, ok := body["reply"].(string)
	if !ok || reply != "test response" {
		t.Fatalf("expected reply 'test response', got %q", reply)
	}

	if mockProto.calls < 1 {
		t.Fatal("mock protocol should have been called")
	}
}

func TestIntegration_ChatEndpointError(t *testing.T) {
	ts, mockProto, _ := setupTestServer(t, func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})
	mockProto.streamFn = func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
		ch := make(chan protocol.Chunk, 1)
		close(ch)
		return ch, io.ErrUnexpectedEOF
	}

	resp, err := http.Post(ts.URL+"/api/chat", "application/json",
		strings.NewReader(`{"message":"hello"}`))
	if err != nil {
		t.Fatalf("POST /api/chat: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)

	reply, ok := body["reply"].(string)
	if !ok || !strings.Contains(reply, "错误") {
		t.Fatalf("expected error reply, got %q", reply)
	}
}

func TestIntegration_StreamEndpoint(t *testing.T) {
	ts, mockProto, _ := setupTestServer(t, func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
		return &protocol.Response{Content: "fallback"}, nil
	})

	resp, err := http.Post(ts.URL+"/api/chat/stream", "application/json",
		strings.NewReader(`{"message":"hello"}`))
	if err != nil {
		t.Fatalf("POST /api/chat/stream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if !strings.Contains(string(body), "hello") || !strings.Contains(string(body), "world") {
		t.Fatalf("expected stream content, got: %s", string(body))
	}

	if mockProto.calls < 1 {
		t.Fatal("mock protocol should have been called")
	}
}

func TestIntegration_Frontend(t *testing.T) {
	ts, _, _ := setupTestServer(t, func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
		return &protocol.Response{Content: "test"}, nil
	})

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if !strings.Contains(string(body), "test") {
		t.Fatalf("expected frontend content, got: %s", string(body))
	}
}

func TestIntegration_InvalidJSON(t *testing.T) {
	ts, _, _ := setupTestServer(t, func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
		return &protocol.Response{Content: "test"}, nil
	})

	resp, err := http.Post(ts.URL+"/api/chat", "application/json",
		strings.NewReader(`not json`))
	if err != nil {
		t.Fatalf("POST /api/chat: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestIntegration_MethodNotAllowed(t *testing.T) {
	ts, _, _ := setupTestServer(t, func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
		return &protocol.Response{Content: "test"}, nil
	})

	resp, err := http.Get(ts.URL + "/api/chat")
	if err != nil {
		t.Fatalf("GET /api/chat: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 405 {
		t.Fatalf("expected 405 for GET, got %d", resp.StatusCode)
	}
}

func TestIntegration_RunnerEvents(t *testing.T) {
	var events []event.Event
	var mu sync.Mutex
	bus := event.NewBus()
	bus.Subscribe(event.AgentStart, func(evt event.Event) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})
	bus.Subscribe(event.AgentEnd, func(evt event.Event) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})

	br := llm.NewBridge(&mockProtoE2E{
		chatFn: func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
			return &protocol.Response{Content: "event test"}, nil
		},
		streamFn: func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
			ch := make(chan protocol.Chunk, 1)
			ch <- protocol.Chunk{Type: protocol.ChunkText, Text: "event test"}
			close(ch)
			return ch, nil
		},
	}, "test-model")
	reg := adktool.NewToolRegistry()
	agent, err := adk.NewReactAgent(context.Background(), &adk.AgentConfig{
		Bridge:    br,
		Tools:     reg,
		ToolNames: []string{},
		Prompt:    "test",
		MaxSteps:  1,
	})
	if err != nil {
		t.Fatalf("NewReactAgent: %v", err)
	}

	runner := adk.NewRunnerWithAgent(agent, nil, nil, nil, bus)
	adapter := &testAgentAdapter{runner: runner}
	mgr := appsession.NewManager("test")
	srv := New(adapter, br, mgr, []byte("<html>test</html>"), &simplePromptProvider{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.HandleChat(w, r)
	}))
	defer ts.Close()

	http.Post(ts.URL, "application/json", strings.NewReader(`{"message":"hi"}`))

	mu.Lock()
	count := len(events)
	mu.Unlock()

	if count != 2 {
		t.Fatalf("expected 2 events (AgentStart+AgentEnd), got %d", count)
	}
}

func TestIntegration_ConcurrentRequests(t *testing.T) {
	ts, mockProto, _ := setupTestServer(t, func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
		return &protocol.Response{Content: "concurrent ok"}, nil
	})
	mockProto.streamFn = func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
		ch := make(chan protocol.Chunk, 1)
		ch <- protocol.Chunk{Type: protocol.ChunkText, Text: "concurrent ok"}
		close(ch)
		return ch, nil
	}

	var wg sync.WaitGroup
	var success atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(ts.URL+"/api/chat", "application/json",
				strings.NewReader(`{"message":"concurrent test"}`))
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				success.Add(1)
			}
		}()
	}
	wg.Wait()

	if success.Load() != 10 {
		t.Fatalf("expected 10 successful requests, got %d", success.Load())
	}
}

func TestIntegration_SessionIsolation(t *testing.T) {
	callCount := 0
	var mu sync.Mutex

	ts, mockProto, _ := setupTestServer(t, func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		return &protocol.Response{
			Content:          "response",
			ReasoningContent: "",
		}, nil
	})
	mockProto.streamFn = func(ctx context.Context, req protocol.Request) (<-chan protocol.Chunk, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		ch := make(chan protocol.Chunk, 1)
		ch <- protocol.Chunk{Type: protocol.ChunkText, Text: "response"}
		close(ch)
		return ch, nil
	}

	sendMsg := func(sid string) {
		req, _ := http.NewRequest("POST", ts.URL+"/api/chat",
			strings.NewReader(`{"message":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		if sid != "" {
			q := req.URL.Query()
			q.Add("session", sid)
			req.URL.RawQuery = q.Encode()
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("request failed: %v", err)
			return
		}
		resp.Body.Close()
	}

	sendMsg("sess_1")
	sendMsg("sess_2")
	sendMsg("sess_1")

	if callCount != 3 {
		t.Fatalf("expected 3 protocol calls, got %d", callCount)
	}
}

func TestIntegration_WithToolCall(t *testing.T) {
	reg := adktool.NewToolRegistry()
	echoTool := adktool.NewFromInvokable(&echoInvokable{})
	reg.Register("echo", echoTool)

	br := llm.NewBridge(&mockProtoE2E{
		chatFn: func(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
			if len(req.Tools) > 0 {
				return &protocol.Response{
					ToolCalls: []protocol.ToolCall{
						{ID: "call_1", Type: "function", Name: "echo", Arguments: `{"msg":"test"}`},
					},
				}, nil
			}
			return &protocol.Response{Content: "final answer"}, nil
		},
	}, "test-model")

	agent, err := adk.NewReactAgent(context.Background(), &adk.AgentConfig{
		Bridge:    br,
		Tools:     reg,
		ToolNames: []string{"echo"},
		Prompt:    "you are a test assistant. When you need to echo, use the echo tool.",
		MaxSteps:  3,
	})
	if err != nil {
		t.Fatalf("NewReactAgent: %v", err)
	}

	runner := adk.NewRunnerWithAgent(agent, nil, nil, nil, nil)
	adapter := &testAgentAdapter{runner: runner}
	mgr := appsession.NewManager("test")
	srv := New(adapter, br, mgr, []byte("<html>test</html>"), &simplePromptProvider{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.HandleChat(w, r)
	}))
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(`{"message":"echo test"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	reply, _ := body["reply"].(string)
	if reply == "" {
		t.Fatal("expected non-empty reply")
	}
}

type echoInvokable struct{}

func (e *echoInvokable) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "echo", Desc: "echoes back the input",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"msg": {Type: schema.String, Desc: "message to echo"},
		}),
	}, nil
}

func (e *echoInvokable) InvokableRun(ctx context.Context, args string, opts ...einotool.Option) (string, error) {
	return "echoed: " + args, nil
}
