package adk

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	adktypes "MiniGoAgent/internal/adk/types"

	"MiniGoAgent/internal/adk/event"
	"MiniGoAgent/internal/adk/middleware"
	"MiniGoAgent/internal/adk/tool"
)

type mockAgent struct {
	runFn    func(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error)
	streamFn func(ctx context.Context, req *adktypes.Request) (<-chan adktypes.Event, error)
}

func (m *mockAgent) Run(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
	return m.runFn(ctx, req)
}

func (m *mockAgent) Stream(ctx context.Context, req *adktypes.Request) (<-chan adktypes.Event, error) {
	return m.streamFn(ctx, req)
}

type mockStore struct {
	data map[string][]*adktypes.Message
}

func newMockStore() *mockStore {
	return &mockStore{data: make(map[string][]*adktypes.Message)}
}

func (s *mockStore) Get(ctx context.Context, sid string) ([]*adktypes.Message, error) {
	return s.data[sid], nil
}

func (s *mockStore) Append(ctx context.Context, sid string, msgs ...*adktypes.Message) error {
	s.data[sid] = append(s.data[sid], msgs...)
	return nil
}

func (s *mockStore) Snapshot(ctx context.Context, sid string) ([]*adktypes.Message, error) {
	out := make([]*adktypes.Message, len(s.data[sid]))
	copy(out, s.data[sid])
	return out, nil
}

func TestRunnerRunWithMiddleware(t *testing.T) {
	var beforeCalled, afterCalled bool

	mw := middleware.New(middleware.MiddlewareFuncs{
		BeforeModelFn: func(ctx context.Context, req *middleware.ModelRequest) (*middleware.ModelRequest, error) {
			beforeCalled = true
			return req, nil
		},
		AfterModelFn: func(ctx context.Context, req *middleware.ModelRequest, resp *middleware.ModelResponse) (*middleware.ModelResponse, error) {
			afterCalled = true
			return resp, nil
		},
	})

	r := NewRunnerWithAgent(&mockAgent{
		runFn: func(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
			return &adktypes.Response{
				Messages: []*adktypes.Message{{Role: adktypes.RoleAssistant, Content: "ok"}},
				Stats:    "test",
			}, nil
		},
	}, nil, nil, mw, nil)

	resp, err := r.Run(context.Background(), &adktypes.Request{
		Messages: []*adktypes.Message{{Role: adktypes.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !beforeCalled {
		t.Fatal("BeforeModel not called")
	}
	if !afterCalled {
		t.Fatal("AfterModel not called")
	}
	if len(resp.Messages) != 1 || resp.Messages[0].Content != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestRunnerRunMiddlewareBlocks(t *testing.T) {
	mw := middleware.New(middleware.MiddlewareFuncs{
		BeforeModelFn: func(ctx context.Context, req *middleware.ModelRequest) (*middleware.ModelRequest, error) {
			return nil, errors.New("blocked by middleware")
		},
	})

	r := NewRunnerWithAgent(&mockAgent{
		runFn: func(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
			t.Fatal("agent should not be called")
			return nil, nil
		},
	}, nil, nil, mw, nil)

	_, err := r.Run(context.Background(), &adktypes.Request{
		Messages: []*adktypes.Message{{Role: adktypes.RoleUser, Content: "hi"}},
	})
	if err == nil || err.Error() != "blocked by middleware" {
		t.Fatalf("expected 'blocked by middleware', got %v", err)
	}
}

func TestRunnerRunWithStore(t *testing.T) {
	store := newMockStore()
	r := NewRunnerWithAgent(&mockAgent{
		runFn: func(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
			return &adktypes.Response{
				Messages: []*adktypes.Message{{Role: adktypes.RoleAssistant, Content: "answer"}},
			}, nil
		},
	}, store, nil, nil, nil)

	_, err := r.Run(context.Background(), &adktypes.Request{
		Messages:  []*adktypes.Message{{Role: adktypes.RoleUser, Content: "q1"}},
		SessionID: "sess_1",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	snap, _ := store.Snapshot(context.Background(), "sess_1")
	if len(snap) != 1 {
		t.Fatalf("expected 1 stored message, got %d", len(snap))
	}
	if snap[0].Content != "answer" {
		t.Fatalf("expected stored 'answer', got %q", snap[0].Content)
	}
}

func TestRunnerRunWithStoreAppendsToExisting(t *testing.T) {
	store := newMockStore()
	store.Append(context.Background(), "sess_2", &adktypes.Message{Role: adktypes.RoleUser, Content: "prior"})

	r := NewRunnerWithAgent(&mockAgent{
		runFn: func(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
			if len(req.Messages) != 2 {
				t.Fatalf("expected 2 messages (1 prior + 1 new), got %d", len(req.Messages))
			}
			return &adktypes.Response{
				Messages: []*adktypes.Message{{Role: adktypes.RoleAssistant, Content: "answer"}},
			}, nil
		},
	}, store, nil, nil, nil)

	_, err := r.Run(context.Background(), &adktypes.Request{
		Messages:  []*adktypes.Message{{Role: adktypes.RoleUser, Content: "q2"}},
		SessionID: "sess_2",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	snap, _ := store.Snapshot(context.Background(), "sess_2")
	if len(snap) != 2 {
		t.Fatalf("expected 2 stored messages (prior+new), got %d", len(snap))
	}
}

func TestRunnerRunWithEvents(t *testing.T) {
	bus := event.NewBus()
	var events []event.Event
	bus.Subscribe(event.AgentStart, func(evt event.Event) {
		events = append(events, evt)
	})
	bus.Subscribe(event.AgentEnd, func(evt event.Event) {
		events = append(events, evt)
	})

	r := NewRunnerWithAgent(&mockAgent{
		runFn: func(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
			return &adktypes.Response{
				Messages: []*adktypes.Message{{Role: adktypes.RoleAssistant, Content: "ok"}},
				Stats:    "gpt-4",
			}, nil
		},
	}, nil, nil, nil, bus)

	_, err := r.Run(context.Background(), &adktypes.Request{
		Messages:  []*adktypes.Message{{Role: adktypes.RoleUser, Content: "hi"}},
		SessionID: "sess_evt",
		ToolNames: []string{"search"},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events (start+end), got %d", len(events))
	}
	if events[0].Type != event.AgentStart {
		t.Fatalf("first event should be AgentStart, got %v", events[0].Type)
	}
	if events[1].Type != event.AgentEnd {
		t.Fatalf("second event should be AgentEnd, got %v", events[1].Type)
	}
}

func TestRunnerRunGuardrailsBlocked(t *testing.T) {
	guards := tool.NewGuardrails(nil)
	guards.AddRule(func(ctx context.Context, name string, args any) tool.GuardrailResult {
		return tool.GuardrailResult{Allowed: false, Reason: "block all"}
	})

	var published atomic.Bool
	bus := event.NewBus()
	bus.Subscribe(event.AgentEnd, func(evt event.Event) {
		published.Store(true)
	})

	r := NewRunnerWithAgent(&mockAgent{
		runFn: func(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
			return &adktypes.Response{}, nil
		},
	}, nil, guards, nil, bus)

	_, err := r.Run(context.Background(), &adktypes.Request{
		Messages: []*adktypes.Message{{Role: adktypes.RoleAssistant, ToolCalls: []adktypes.ToolCall{{Name: "any_tool"}}}},
	})
	if err == nil {
		t.Fatal("expected guardrails error")
	}
	if published.Load() {
		t.Fatal("should not publish AgentEnd when guardrails block")
	}
}

func TestRunnerRunErrorEvent(t *testing.T) {
	bus := event.NewBus()
	var errEvent event.Event
	bus.Subscribe(event.Error, func(evt event.Event) {
		errEvent = evt
	})

	r := NewRunnerWithAgent(&mockAgent{
		runFn: func(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
			return nil, errors.New("agent error")
		},
	}, nil, nil, nil, bus)

	_, err := r.Run(context.Background(), &adktypes.Request{
		Messages: []*adktypes.Message{{Role: adktypes.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if errEvent.Type != event.Error {
		t.Fatalf("expected Error event, got %v", errEvent.Type)
	}
}

func TestRunnerStream(t *testing.T) {
	r := NewRunnerWithAgent(&mockAgent{
		streamFn: func(ctx context.Context, req *adktypes.Request) (<-chan adktypes.Event, error) {
			ch := make(chan adktypes.Event, 1)
			ch <- adktypes.Event{Type: adktypes.EventText, Content: "hello"}
			close(ch)
			return ch, nil
		},
	}, nil, nil, nil, nil)

	events, err := r.Stream(context.Background(), &adktypes.Request{
		Messages: []*adktypes.Message{{Role: adktypes.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	var count int
	for range events {
		count++
	}
	if count != 1 {
		t.Fatalf("expected 1 event, got %d", count)
	}
}

func TestRunnerRunNilResponse(t *testing.T) {
	r := NewRunnerWithAgent(&mockAgent{
		runFn: func(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
			return nil, nil
		},
	}, nil, nil, nil, nil)

	resp, err := r.Run(context.Background(), &adktypes.Request{
		Messages: []*adktypes.Message{{Role: adktypes.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response even when agent returns nil")
	}
}

func TestRunnerStreamContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	r := NewRunnerWithAgent(&mockAgent{
		streamFn: func(ctx context.Context, req *adktypes.Request) (<-chan adktypes.Event, error) {
			ch := make(chan adktypes.Event)
			go func() {
				defer close(ch)
				for {
					select {
					case <-ctx.Done():
						return
					case ch <- adktypes.Event{Type: adktypes.EventText, Content: "chunk"}:
					}
				}
			}()
			return ch, nil
		},
	}, nil, nil, nil, nil)

	events, err := r.Stream(ctx, &adktypes.Request{
		Messages: []*adktypes.Message{{Role: adktypes.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	<-events
	cancel()

	done := make(chan struct{})
	go func() {
		for range events {
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not terminate after context cancel (goroutine leak)")
	}
}

func TestRunnerStreamPublishesAgentEnd(t *testing.T) {
	bus := event.NewBus()
	var ended atomic.Bool
	bus.Subscribe(event.AgentEnd, func(evt event.Event) {
		ended.Store(true)
	})

	r := NewRunnerWithAgent(&mockAgent{
		streamFn: func(ctx context.Context, req *adktypes.Request) (<-chan adktypes.Event, error) {
			ch := make(chan adktypes.Event, 1)
			ch <- adktypes.Event{Type: adktypes.EventText, Content: "hi"}
			close(ch)
			return ch, nil
		},
	}, nil, nil, nil, bus)

	events, err := r.Stream(context.Background(), &adktypes.Request{
		Messages: []*adktypes.Message{{Role: adktypes.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	for range events {
	}

	if !ended.Load() {
		t.Fatal("AgentEnd not published after stream drained")
	}
}

func TestRunnerStreamBeforeModelRuns(t *testing.T) {
	var beforeCalled atomic.Bool
	mw := middleware.New(middleware.MiddlewareFuncs{
		BeforeModelFn: func(ctx context.Context, req *middleware.ModelRequest) (*middleware.ModelRequest, error) {
			beforeCalled.Store(true)
			return req, nil
		},
	})

	r := NewRunnerWithAgent(&mockAgent{
		streamFn: func(ctx context.Context, req *adktypes.Request) (<-chan adktypes.Event, error) {
			ch := make(chan adktypes.Event)
			close(ch)
			return ch, nil
		},
	}, nil, nil, mw, nil)

	events, err := r.Stream(context.Background(), &adktypes.Request{
		Messages: []*adktypes.Message{{Role: adktypes.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	for range events {
	}
	if !beforeCalled.Load() {
		t.Fatal("BeforeModel not called in stream path")
	}
}

func TestRunnerStreamBeforeModelBlocks(t *testing.T) {
	mw := middleware.New(middleware.MiddlewareFuncs{
		BeforeModelFn: func(ctx context.Context, req *middleware.ModelRequest) (*middleware.ModelRequest, error) {
			return nil, errors.New("blocked")
		},
	})

	r := NewRunnerWithAgent(&mockAgent{
		streamFn: func(ctx context.Context, req *adktypes.Request) (<-chan adktypes.Event, error) {
			t.Fatal("agent should not be called")
			return nil, nil
		},
	}, nil, nil, mw, nil)

	_, err := r.Stream(context.Background(), &adktypes.Request{
		Messages: []*adktypes.Message{{Role: adktypes.RoleUser, Content: "hi"}},
	})
	if err == nil || err.Error() != "blocked" {
		t.Fatalf("expected 'blocked', got %v", err)
	}
}

func TestRunnerAllIntegrations(t *testing.T) {
	store := newMockStore()
	bus := event.NewBus()

	var mwCalled atomic.Bool
	mw := middleware.New(middleware.MiddlewareFuncs{
		BeforeModelFn: func(ctx context.Context, req *middleware.ModelRequest) (*middleware.ModelRequest, error) {
			mwCalled.Store(true)
			return req, nil
		},
	})

	r := NewRunnerWithAgent(&mockAgent{
		runFn: func(ctx context.Context, req *adktypes.Request) (*adktypes.Response, error) {
			return &adktypes.Response{
				Messages: []*adktypes.Message{{Role: adktypes.RoleAssistant, Content: "integrated"}},
			}, nil
		},
	}, store, nil, mw, bus)

	_, err := r.Run(context.Background(), &adktypes.Request{
		Messages:  []*adktypes.Message{{Role: adktypes.RoleUser, Content: "test"}},
		SessionID: "sess_integrated",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !mwCalled.Load() {
		t.Fatal("middleware not called")
	}

	snap, _ := store.Snapshot(context.Background(), "sess_integrated")
	if len(snap) == 0 {
		t.Fatal("store should have messages")
	}
}
