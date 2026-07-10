package protocol

import (
	"context"
	"sync"
)

type ChunkProcessor interface {
	Process(ctx context.Context, chunk Chunk) error
	Name() string
}

type EventBus struct {
	publisher chan Chunk
	subs      map[string]chan Chunk
	mu        sync.RWMutex
	wg        sync.WaitGroup
}

func NewEventBus(ctx context.Context, bufferSize int) *EventBus {
	eb := &EventBus{
		publisher: make(chan Chunk, bufferSize),
		subs:      make(map[string]chan Chunk),
	}
	eb.wg.Add(1)
	go eb.run()
	return eb
}

func (eb *EventBus) Subscribe(name string, p ChunkProcessor) error {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if _, ok := eb.subs[name]; ok {
		return nil
	}
	ch := make(chan Chunk, 256)
	eb.subs[name] = ch
	eb.wg.Add(1)
	go eb.dispatch(name, p, ch)
	return nil
}

func (eb *EventBus) Publish(ctx context.Context, chunk Chunk) error {
	select {
	case eb.publisher <- chunk:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (eb *EventBus) Stop() {
	close(eb.publisher)
	eb.wg.Wait()
}

func (eb *EventBus) run() {
	defer eb.wg.Done()
	for chunk := range eb.publisher {
		eb.mu.RLock()
		for _, ch := range eb.subs {
			select {
			case ch <- chunk:
			default:
			}
		}
		eb.mu.RUnlock()
	}
}

func (eb *EventBus) dispatch(name string, p ChunkProcessor, ch <-chan Chunk) {
	defer eb.wg.Done()
	for chunk := range ch {
		_ = p.Process(context.Background(), chunk)
	}
}

type telemetryProcessor struct {
	telemetry *Telemetry
}

func (p *telemetryProcessor) Process(_ context.Context, chunk Chunk) error {
	switch chunk.Type {
	case ChunkText:
		p.telemetry.RecordChunkBytes(int64(len(chunk.Text)))
	case ChunkReasoning:
		p.telemetry.RecordChunkBytes(int64(len(chunk.Text)))
	case ChunkToolCall, ChunkToolCallStart:
		p.telemetry.RecordToolCall()
	case ChunkUsage:
		if chunk.Usage != nil {
			p.telemetry.RecordUsage(chunk.Usage)
		}
	case ChunkError:
		p.telemetry.RecordError()
	}
	return nil
}

func (p *telemetryProcessor) Name() string { return "telemetry" }

type logProcessor struct {
	logf func(ctx context.Context, format string, args ...any)
}

func (p *logProcessor) Process(_ context.Context, chunk Chunk) error {
	switch chunk.Type {
	case ChunkText:
		if len(chunk.Text) > 0 {
			p.logf(context.Background(), "RAW chunk text len=%d", len(chunk.Text))
		}
	case ChunkReasoning:
		if len(chunk.Text) > 0 {
			p.logf(context.Background(), "RAW chunk reasoning len=%d", len(chunk.Text))
		}
	case ChunkToolCallStart:
		if chunk.ToolCall != nil {
			p.logf(context.Background(), "RAW tool_call_start id=%s name=%s", chunk.ToolCall.ID, chunk.ToolCall.Name)
		}
	case ChunkToolCall:
		if chunk.ToolCall != nil {
			p.logf(context.Background(), "RAW tool_call id=%s name=%s args=%s", chunk.ToolCall.ID, chunk.ToolCall.Name, chunk.ToolCall.Arguments)
		}
	case ChunkUsage:
		if chunk.Usage != nil {
			p.logf(context.Background(), "RAW usage prompt=%d completion=%d total=%d", chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens, chunk.Usage.TotalTokens)
		}
	case ChunkWarn:
		p.logf(context.Background(), "RAW warn: %s", chunk.Warning)
	case ChunkError:
		if chunk.Error != nil {
			p.logf(context.Background(), "RAW error: %v", chunk.Error)
		}
	}
	return nil
}

func (p *logProcessor) Name() string { return "log" }
