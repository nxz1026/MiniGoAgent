package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"MiniGoAgent/protocol"
)

type ChatModel struct {
	proto protocol.Protocol
	tools []*schema.ToolInfo
	model string
}

func NewChatModel(proto protocol.Protocol, model string) *ChatModel {
	return &ChatModel{proto: proto, model: model}
}

func (m *ChatModel) StatsLine() string {
	type telProvider interface{ GetTelemetry() *protocol.Telemetry }
	p, ok := m.proto.(telProvider)
	if !ok {
		return m.model
	}
	return p.GetTelemetry().FormatLine(" · ")
}

func (m *ChatModel) StatsJSON() string {
	type telProvider interface{ GetTelemetry() *protocol.Telemetry }
	p, ok := m.proto.(telProvider)
	if !ok {
		return ""
	}
	data, err := json.Marshal(p.GetTelemetry().FormatMap())
	if err != nil {
		return ""
	}
	return string(data)
}

func ToProtoMsg(m *schema.Message) protocol.Message {
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

func ToProtoMsgs(msgs []*schema.Message) []protocol.Message {
	out := make([]protocol.Message, len(msgs))
	for i, m := range msgs {
		out[i] = ToProtoMsg(m)
	}
	return out
}

func ToProtoTools(tools []*schema.ToolInfo) []protocol.ToolSchema {
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

func FromProtoResp(resp *protocol.Response) *schema.Message {
	msg := &schema.Message{Role: schema.Assistant, Content: resp.Content, ReasoningContent: resp.ReasoningContent}
	for _, tc := range resp.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID: tc.ID, Type: tc.Type,
			Function: schema.FunctionCall{Name: tc.Name, Arguments: tc.Arguments},
		})
	}
	return msg
}

func (m *ChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	common := model.GetCommonOptions(nil, opts...)
	req := protocol.Request{
		Messages:  ToProtoMsgs(input),
		Tools:     ToProtoTools(m.tools),
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

	resp, err := m.proto.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		return nil, fmt.Errorf("API返回空消息")
	}
	return FromProtoResp(resp), nil
}

func (m *ChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	common := model.GetCommonOptions(nil, opts...)
	req := protocol.Request{
		Messages:  ToProtoMsgs(input),
		Tools:     ToProtoTools(m.tools),
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

	ch, err := m.proto.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[*schema.Message](64)
	go func() {
		defer sw.Close()
		var (
			fullContent   string
			fullReasoning string
			toolCalls     []schema.ToolCall
			interrupted   bool
		)
		for chunk := range ch {
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
				sw.Send(msg, nil)
				fullContent = ""
				fullReasoning = ""
				toolCalls = nil
			case protocol.ChunkError:
				var streamErr *protocol.StreamInterruptedError
				if chunk.Error != nil && errors.As(chunk.Error, &streamErr) {
					interrupted = true
					sw.Send(&schema.Message{Role: schema.Assistant, Content: "[流中断，正在恢复...]"}, nil)
					resp, err := m.forwardChat(ctx, req)
					if err != nil {
						sw.Send(nil, err)
						return
					}
					sw.Send(FromProtoResp(resp), nil)
				}
				return
			}
		}
		if !interrupted {
			msg := &schema.Message{Role: schema.Assistant, Content: fullContent, ReasoningContent: fullReasoning}
			if len(toolCalls) > 0 {
				msg.ToolCalls = toolCalls
			}
			sw.Send(msg, nil)
		}
	}()
	return sr, nil
}

func (m *ChatModel) forwardChat(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
	return m.proto.Chat(ctx, req)
}

func (m *ChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &ChatModel{proto: m.proto, model: m.model, tools: tools}, nil
}
