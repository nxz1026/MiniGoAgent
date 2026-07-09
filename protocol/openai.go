package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

func init() {
	Register("openai", func(cfg Config) (Protocol, error) {
		return NewOpenAI(cfg.APIKey, cfg.BaseURL, cfg.Model), nil
	})
}

type reasoningPolicy int

const (
	reasoningNone      reasoningPolicy = iota
	reasoningEffort
	reasoningThinking
)

type OpenAI struct {
	apiKey       string
	baseURL      string
	model        string
	client       *http.Client
	name         string
	vendor       Vendor
	effort       string
	thinkingType string
	policy       reasoningPolicy
}

func NewOpenAI(apiKey, baseURL, model string) *OpenAI {
	baseURL = strings.TrimRight(baseURL, "/")
	o := &OpenAI{
		apiKey:  apiKey,
		baseURL: baseURL + "/chat/completions",
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
		name:    model,
		vendor:  DetectVendor(baseURL),
	}
	switch o.vendor {
	case VendorDeepSeek:
		o.policy = reasoningThinking
	case VendorZhipu, VendorMiniMax, VendorLongCat:
		o.policy = reasoningThinking
	default:
		o.policy = reasoningEffort
	}
	return o
}

func (o *OpenAI) Chat(ctx context.Context, req Request) (*Response, error) {
	ch, err := o.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	var resp Response
	for chunk := range ch {
		switch chunk.Type {
		case ChunkText:
			resp.Content += chunk.Text
		case ChunkReasoning:
			resp.ReasoningContent += chunk.Text
		case ChunkToolCall:
			resp.ToolCalls = append(resp.ToolCalls, *chunk.ToolCall)
		case ChunkError:
			return nil, fmt.Errorf("%s", chunk.Error)
		}
	}
	return &resp, nil
}

const maxReconnectAttempts = 3

func (o *OpenAI) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	out := make(chan Chunk, 64)
	go o.streamWithReconnect(ctx, req, out)
	return out, nil
}

func (o *OpenAI) streamWithReconnect(ctx context.Context, req Request, out chan<- Chunk) {
	var emitted bool
	var lastErr error
	for attempt := 0; attempt <= maxReconnectAttempts; attempt++ {
		if attempt > 0 {
			if emitted {
				sendChunk(ctx, out, Chunk{Type: ChunkError, Error: lastErr.Error()})
				return
			}
			delay := backoffDelay(attempt, 0)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		resp, err := SendWithRetry(ctx, o.client, o.vendor, func(ctx context.Context) (*http.Request, error) {
			return o.buildHTTPRequest(ctx, req)
		})
		if err != nil {
			lastErr = err
			continue
		}
		emitted, err = o.readStream(ctx, resp, out)
		if err != nil {
			lastErr = err
			if emitted || !IsConnReset(err) {
				if !emitted {
					sendChunk(ctx, out, Chunk{Type: ChunkError, Error: err.Error()})
				}
				return
			}
			continue
		}
		return
	}
	sendChunk(ctx, out, Chunk{Type: ChunkError, Error: lastErr.Error()})
}

func sendChunk(ctx context.Context, out chan<- Chunk, ch Chunk) {
	select {
	case out <- ch:
	case <-ctx.Done():
	}
}

func (o *OpenAI) buildHTTPRequest(ctx context.Context, req Request) (*http.Request, error) {
	body := o.buildBody(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	return httpReq, nil
}

func (o *OpenAI) buildBody(req Request) []byte {
	m := map[string]any{
		"model":    o.model,
		"messages": o.toWireMessages(NormalizeMessages(req.Messages)),
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		m["tools"] = o.toWireTools(req.Tools)
	}
	if req.Temperature != nil {
		m["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		m["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		m["max_tokens"] = *req.MaxTokens
	}
	if len(req.Stop) > 0 {
		m["stop"] = req.Stop
	}
	switch o.policy {
	case reasoningThinking:
		switch o.vendor {
		case VendorDeepSeek:
			if o.thinkingType == "disabled" {
				m["thinking"] = map[string]string{"type": "disabled"}
			} else {
				m["thinking"] = map[string]string{"type": "enabled"}
			}
		case VendorMiniMax:
			t := o.effort
			if t == "" {
				t = "adaptive"
			}
			m["thinking"] = map[string]string{"type": t}
		case VendorZhipu:
			t := o.effort
			if t == "" {
				t = "enabled"
			}
			m["thinking"] = map[string]string{"type": t}
		case VendorLongCat:
			t := o.thinkingType
			if t == "" {
				t = "enabled"
			}
			m["thinking"] = map[string]string{"type": t}
		}
	case reasoningEffort:
		if o.effort != "" {
			m["reasoning_effort"] = o.effort
		}
	}
	body, _ := json.Marshal(m)
	return body
}

func (o *OpenAI) toWireMessages(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		item := map[string]any{"role": string(m.Role), "content": m.Content}
		if len(m.ToolCalls) > 0 {
			tcs := make([]map[string]any, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				tcs[i] = map[string]any{
					"id": tc.ID, "type": "function",
					"function": map[string]string{"name": tc.Name, "arguments": tc.Arguments},
				}
			}
			item["tool_calls"] = tcs
			if o.vendor == VendorDeepSeek {
				item["reasoning_content"] = m.ReasoningContent
			}
		}
		if m.ToolCallID != "" {
			item["tool_call_id"] = m.ToolCallID
		}
		if m.Name != "" {
			item["name"] = m.Name
		}
		out = append(out, item)
	}
	return out
}

func (o *OpenAI) toWireTools(tools []ToolSchema) []map[string]any {
	out := make([]map[string]any, len(tools))
	for i, t := range tools {
		params := CanonicalizeSchema(t.Parameters)
		out[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": params,
			},
		}
	}
	return out
}

func (o *OpenAI) readStream(ctx context.Context, resp *http.Response, out chan<- Chunk) (emitted bool, _ error) {
	defer resp.Body.Close()
	defer close(out)

	idleTimeout := 120 * time.Second
	done := make(chan struct{})
	defer close(done)
	activity := make(chan struct{}, 1)
	var stalled atomic.Bool
	go func() {
		idle := time.NewTimer(idleTimeout)
		defer idle.Stop()
		for {
			select {
			case <-ctx.Done():
				resp.Body.Close()
				return
			case <-idle.C:
				stalled.Store(true)
				resp.Body.Close()
				return
			case <-activity:
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(idleTimeout)
			case <-done:
				return
			}
		}
	}()

	acc := map[int]*ToolCall{}
	started := map[int]bool{}
	var order []int
	var think thinkSplitter

	send := func(ch Chunk) bool {
		select {
		case out <- ch:
			emitted = true
			return true
		case <-ctx.Done():
			return false
		}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case activity <- struct{}{}:
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			send(Chunk{Type: ChunkDone})
			break
		}

		var sr struct {
			Choices []struct {
				Delta struct {
					Content          string                   `json:"content"`
					ReasoningContent string                   `json:"reasoning_content"`
					Reasoning        string                   `json:"reasoning"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
			Error *struct{ Message string } `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &sr); err != nil {
			continue
		}
		if sr.Error != nil {
			send(Chunk{Type: ChunkError, Error: sr.Error.Message})
			return emitted, fmt.Errorf("API stream error: %s", sr.Error.Message)
		}
		if sr.Usage != nil {
			send(Chunk{Type: ChunkUsage, Usage: &Usage{
				PromptTokens:     sr.Usage.PromptTokens,
				CompletionTokens: sr.Usage.CompletionTokens,
				TotalTokens:      sr.Usage.TotalTokens,
			}})
		}
		if len(sr.Choices) == 0 {
			continue
		}

		delta := sr.Choices[0].Delta

		reasoningDelta := delta.ReasoningContent
		if reasoningDelta == "" {
			reasoningDelta = delta.Reasoning
		}
		if reasoningDelta != "" {
			if !send(Chunk{Type: ChunkReasoning, Text: reasoningDelta}) {
				return emitted, ctx.Err()
			}
		}

		if delta.Content != "" {
			if o.vendor == VendorMiniMax {
				r, txt := think.push(delta.Content)
				if r != "" {
					if !send(Chunk{Type: ChunkReasoning, Text: r}) {
						return emitted, ctx.Err()
					}
				}
				if txt != "" {
					if !send(Chunk{Type: ChunkText, Text: txt}) {
						return emitted, ctx.Err()
					}
				}
			} else {
				if !send(Chunk{Type: ChunkText, Text: delta.Content}) {
					return emitted, ctx.Err()
				}
			}
		}

		for _, tc := range delta.ToolCalls {
			cur, ok := acc[tc.Index]
			if !ok {
				cur = &ToolCall{}
				acc[tc.Index] = cur
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				cur.ID = tc.ID
			}
			if tc.Function.Name != "" {
				cur.Name = tc.Function.Name
			}
			cur.Arguments += tc.Function.Arguments
			if !started[tc.Index] && cur.Name != "" {
				started[tc.Index] = true
				if !send(Chunk{Type: ChunkToolCallStart, ToolCall: &ToolCall{ID: cur.ID, Name: cur.Name}}) {
					return emitted, ctx.Err()
				}
			}
		}

		if sr.Choices[0].FinishReason != nil && *sr.Choices[0].FinishReason != "" {
			r, _ := think.flush()
			if r != "" {
				if !send(Chunk{Type: ChunkReasoning, Text: r}) {
					return emitted, ctx.Err()
				}
			}
			for _, idx := range order {
				if tc := acc[idx]; tc != nil {
					if tc.ID == "" {
						tc.ID = fmt.Sprintf("call_%d", idx)
					}
					if !send(Chunk{Type: ChunkToolCall, ToolCall: tc}) {
						return emitted, ctx.Err()
					}
				}
			}
			send(Chunk{Type: ChunkDone})
			return emitted, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return emitted, fmt.Errorf("scanner error: %w", err)
	}
	if stalled.Load() {
		send(Chunk{Type: ChunkError, Error: "stream idle timeout"})
		return emitted, fmt.Errorf("stream idle timeout")
	}
	return emitted, nil
}
