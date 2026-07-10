package convert

import (
	"github.com/cloudwego/eino/schema"

	adktypes "MiniGoAgent/internal/adk/types"
)

// ToEino 将 adk 消息转换为 eino schema.Message。
func ToEino(m *adktypes.Message) *schema.Message {
	if m == nil {
		return nil
	}
	msg := &schema.Message{
		Role:             schema.RoleType(m.Role),
		Content:          m.Content,
		ReasoningContent: m.ReasoningContent,
		ToolCallID:       m.ToolCallID,
		Name:             m.Name,
	}
	for _, tc := range m.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: schema.FunctionCall{
				Name:      tc.Name,
				Arguments: tc.Arguments,
			},
		})
	}
	return msg
}

// FromEino 将 eino schema.Message 转换为 adk 消息。
func FromEino(m *schema.Message) *adktypes.Message {
	if m == nil {
		return nil
	}
	msg := &adktypes.Message{
		Role:             adktypes.Role(m.Role),
		Content:          m.Content,
		ReasoningContent: m.ReasoningContent,
		ToolCallID:       m.ToolCallID,
		Name:             m.Name,
	}
	for _, tc := range m.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, adktypes.ToolCall{
			ID:        tc.ID,
			Type:      tc.Type,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return msg
}

// ToEinoSlice 批量转换 adk -> eino。
func ToEinoSlice(msgs []*adktypes.Message) []*schema.Message {
	out := make([]*schema.Message, len(msgs))
	for i, m := range msgs {
		out[i] = ToEino(m)
	}
	return out
}

// FromEinoSlice 批量转换 eino -> adk。
func FromEinoSlice(msgs []*schema.Message) []*adktypes.Message {
	out := make([]*adktypes.Message, len(msgs))
	for i, m := range msgs {
		out[i] = FromEino(m)
	}
	return out
}
