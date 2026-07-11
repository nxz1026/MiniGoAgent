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
	if len(m.MultiContent) > 0 {
		msg.UserInputMultiContent = toEinoMultiContent(m.MultiContent)
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

func toEinoMultiContent(mc []map[string]any) []schema.MessageInputPart {
	out := make([]schema.MessageInputPart, 0, len(mc))
	for _, item := range mc {
		typ, _ := item["type"].(string)
		part := schema.MessageInputPart{Type: schema.ChatMessagePartType(typ)}
		if text, ok := item["text"].(string); ok {
			part.Text = text
		}
		if imgRaw, ok := item["image_url"]; ok {
			if img, ok := imgRaw.(map[string]any); ok {
				url, _ := img["url"].(string)
				part.Image = &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{URL: &url},
				}
			}
		}
		out = append(out, part)
	}
	return out
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
	if len(m.UserInputMultiContent) > 0 {
		msg.MultiContent = fromEinoMultiContent(m.UserInputMultiContent)
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

func fromEinoMultiContent(mc []schema.MessageInputPart) []map[string]any {
	out := make([]map[string]any, 0, len(mc))
	for _, part := range mc {
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
		out = append(out, item)
	}
	return out
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
