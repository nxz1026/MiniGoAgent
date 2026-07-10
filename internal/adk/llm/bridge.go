package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"MiniGoAgent/protocol"
)

type Bridge struct {
	proto protocol.Protocol
	model string
	tools []*schema.ToolInfo
}

func NewBridge(proto protocol.Protocol, model string) *Bridge {
	return &Bridge{proto: proto, model: model}
}

func (b *Bridge) StatsLine() string {
	type telProvider interface{ GetTelemetry() *protocol.Telemetry }
	p, ok := b.proto.(telProvider)
	if !ok {
		return b.model
	}
	return p.GetTelemetry().FormatLine(" · ")
}

func (b *Bridge) StatsJSON() string {
	type telProvider interface{ GetTelemetry() *protocol.Telemetry }
	p, ok := b.proto.(telProvider)
	if !ok {
		return ""
	}
	data, _ := json.Marshal(p.GetTelemetry().FormatMap())
	return string(data)
}

func (b *Bridge) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return b.generate(ctx, input, opts...)
}

func (b *Bridge) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return b.stream(ctx, input, opts...)
}

func (b *Bridge) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *b
	cp.tools = tools
	return &cp, nil
}

func (b *Bridge) Model() string { return b.model }

func (b *Bridge) Protocol() protocol.Protocol { return b.proto }

func (b *Bridge) generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	req := buildRequest(input, b.tools, opts...)
	resp, err := b.proto.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		return nil, fmt.Errorf("API returned empty message")
	}
	return fromProtoResp(resp), nil
}

func (b *Bridge) stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	req := buildRequest(input, b.tools, opts...)
	ch, err := b.proto.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[*schema.Message](64)
	go func() {
		defer sw.Close()
		var (
			fullContent      string
			fullReasoning    string
			toolCalls        []schema.ToolCall
			streamInterrupted bool
		)
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-ch:
				if !ok {
					if !streamInterrupted {
						msg := &schema.Message{Role: schema.Assistant, Content: fullContent, ReasoningContent: fullReasoning}
						if len(toolCalls) > 0 {
							msg.ToolCalls = toolCalls
						}
						sw.Send(msg, nil)
					}
					return
				}
				switch chunk.Type {
				case protocol.ChunkText:
					fullContent += chunk.Text
				case protocol.ChunkReasoning:
					fullReasoning += chunk.Text
				case protocol.ChunkToolCallStart:
					if chunk.ToolCall != nil {
						toolCalls = append(toolCalls, schema.ToolCall{
							ID:   chunk.ToolCall.ID,
							Type: "function",
							Function: schema.FunctionCall{Name: chunk.ToolCall.Name},
						})
					}
				case protocol.ChunkToolCall:
					for i := range toolCalls {
						if toolCalls[i].ID == chunk.ToolCall.ID {
							toolCalls[i].Function.Arguments = chunk.ToolCall.Arguments
							break
						}
					}
				case protocol.ChunkDone:
					msg := &schema.Message{Role: schema.Assistant, Content: fullContent, ReasoningContent: fullReasoning}
					if len(toolCalls) > 0 {
						msg.ToolCalls = toolCalls
					}
					if sw.Send(msg, nil) {
						return
					}
				case protocol.ChunkError:
					var interrupted *protocol.StreamInterruptedError
					if chunk.Error != nil && errors.As(chunk.Error, &interrupted) {
						streamInterrupted = true
						sw.Send(&schema.Message{Role: schema.Assistant, Content: "[流中断，正在恢复...]"}, nil)
						resp, err := b.proto.Chat(ctx, req)
						if err != nil {
							return
						}
						sw.Send(fromProtoResp(resp), nil)
					}
					return
				}
			}
		}
	}()
	return sr, nil
}

func buildRequest(input []*schema.Message, tools []*schema.ToolInfo, opts ...model.Option) protocol.Request {
	common := model.GetCommonOptions(nil, opts...)
	req := protocol.Request{
		Messages:  toProtoMsgs(input),
		Tools:     toProtoTools(tools),
		MaxTokens: common.MaxTokens,
		Stop:      common.Stop,
	}
	if common.Temperature != nil {
		v := float64(*common.Temperature)
		req.Temperature = &v
	}
	if common.TopP != nil {
		v := float64(*common.TopP)
		req.TopP = &v
	}
	return req
}

func toProtoMsg(m *schema.Message) protocol.Message {
	pm := protocol.Message{
		Role:             protocol.Role(m.Role),
		Content:          m.Content,
		ReasoningContent: m.ReasoningContent,
		ToolCallID:       m.ToolCallID,
		Name:             m.Name,
	}
	if len(m.UserInputMultiContent) > 0 {
		pm.MultiContent = make([]map[string]any, 0, len(m.UserInputMultiContent))
		for _, part := range m.UserInputMultiContent {
			item := map[string]any{"type": string(part.Type)}
			if part.Text != "" {
				item["text"] = part.Text
			}
			if part.Image != nil {
				img := map[string]any{}
				if part.Image.URL != nil {
					img["url"] = *part.Image.URL
				}
				if part.Image.Base64Data != nil {
					img["url"] = "data:" + part.Image.MIMEType + ";base64," + *part.Image.Base64Data
				}
				item["image_url"] = img
			}
			pm.MultiContent = append(pm.MultiContent, item)
		}
	}
	for _, tc := range m.ToolCalls {
		pm.ToolCalls = append(pm.ToolCalls, protocol.ToolCall{
			ID: tc.ID, Type: tc.Type, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
		})
	}
	return pm
}

func toProtoMsgs(msgs []*schema.Message) []protocol.Message {
	out := make([]protocol.Message, len(msgs))
	for i, m := range msgs {
		out[i] = toProtoMsg(m)
	}
	return out
}

func toProtoTools(tools []*schema.ToolInfo) []protocol.ToolSchema {
	out := make([]protocol.ToolSchema, len(tools))
	for i, t := range tools {
		params := json.RawMessage(`{"type":"object","properties":{}}`)
		if js, err := t.ParamsOneOf.ToJSONSchema(); err == nil && js != nil {
			if b, err := json.Marshal(js); err == nil {
				params = json.RawMessage(b)
			}
		}
		out[i] = protocol.ToolSchema{Name: t.Name, Description: t.Desc, Parameters: params}
	}
	return out
}

func fromProtoResp(resp *protocol.Response) *schema.Message {
	msg := &schema.Message{Role: schema.Assistant, Content: resp.Content, ReasoningContent: resp.ReasoningContent}
	for _, tc := range resp.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID: tc.ID, Type: tc.Type,
			Function: schema.FunctionCall{Name: tc.Name, Arguments: tc.Arguments},
		})
	}
	return msg
}
