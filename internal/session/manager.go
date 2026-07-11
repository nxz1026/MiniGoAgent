package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

const DefaultHistoryFile = "history.json"

type Manager struct {
	mu             sync.Mutex
	sessions       map[string][]*schema.Message
	lastAccess     map[string]time.Time
	defaultSession string
}

func NewManager(defaultSession string) *Manager {
	if defaultSession == "" {
		defaultSession = "default"
	}
	return &Manager{
		sessions:       map[string][]*schema.Message{},
		lastAccess:     map[string]time.Time{},
		defaultSession: defaultSession,
	}
}

func (m *Manager) DefaultSession() string {
	return m.defaultSession
}

func (m *Manager) SnapshotWith(sid string, extra ...*schema.Message) []*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastAccess[sid] = time.Now()
	msgs := append([]*schema.Message(nil), m.sessions[sid]...)
	return append(msgs, extra...)
}

func (m *Manager) Append(sid string, msgs ...*schema.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[sid] = append(m.sessions[sid], msgs...)
	m.lastAccess[sid] = time.Now()
}

func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *Manager) Cleanup(maxAge time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	removed := 0
	for sid, last := range m.lastAccess {
		if now.Sub(last) > maxAge {
			delete(m.sessions, sid)
			delete(m.lastAccess, sid)
			removed++
		}
	}
	return removed
}

func (m *Manager) StartCleanup(ctx context.Context, interval time.Duration, maxAge time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				removed := m.Cleanup(maxAge)
				if removed > 0 {
					// 静默清理，日志由底层框架处理
				}
			}
		}
	}()
}

func (m *Manager) Save(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) == 0 {
		return nil
	}
	data, err := Marshal(m.sessions)
	if err != nil {
		return fmt.Errorf("marshal sessions: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	return nil
}

func (m *Manager) Load(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	sessions, err := Unmarshal(data)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range sessions {
		m.sessions[k] = v
	}
	return len(sessions), nil
}

type historyToolCall struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type historyMessage struct {
	Role             string            `json:"role"`
	Content          string            `json:"content"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ToolCalls        []historyToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	Name             string            `json:"name,omitempty"`
	MultiContent     []map[string]any  `json:"multi_content,omitempty"`
}

func toHistoryMsg(m *schema.Message) historyMessage {
	hm := historyMessage{
		Role:             string(m.Role),
		Content:          m.Content,
		ReasoningContent: m.ReasoningContent,
		ToolCallID:       m.ToolCallID,
		Name:             m.Name,
	}
	if len(m.UserInputMultiContent) > 0 {
		hm.MultiContent = fromEinoMultiContent(m.UserInputMultiContent)
	} else if len(m.MultiContent) > 0 {
		hm.MultiContent = chatMessagePartsToMaps(m.MultiContent)
	}
	for _, tc := range m.ToolCalls {
		hm.ToolCalls = append(hm.ToolCalls, historyToolCall{
			ID: tc.ID, Type: tc.Type, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
		})
	}
	return hm
}

func fromHistoryMsg(hm historyMessage) *schema.Message {
	m := &schema.Message{
		Role:             schema.RoleType(hm.Role),
		Content:          hm.Content,
		ReasoningContent: hm.ReasoningContent,
		ToolCallID:       hm.ToolCallID,
		Name:             hm.Name,
	}
	if len(hm.MultiContent) > 0 {
		m.UserInputMultiContent = toEinoMultiContent(hm.MultiContent)
	}
	for _, tc := range hm.ToolCalls {
		m.ToolCalls = append(m.ToolCalls, schema.ToolCall{
			ID: tc.ID, Type: tc.Type,
			Function: schema.FunctionCall{Name: tc.Name, Arguments: tc.Arguments},
		})
	}
	return m
}

func chatMessagePartsToMaps(parts []schema.ChatMessagePart) []map[string]any {
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		item := map[string]any{"type": string(p.Type)}
		if p.Text != "" {
			item["text"] = p.Text
		}
		if p.ImageURL != nil {
			item["image_url"] = map[string]any{"url": p.ImageURL.URL}
		}
		out = append(out, item)
	}
	return out
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

func Marshal(sessions map[string][]*schema.Message) ([]byte, error) {
	dto := make(map[string][]historyMessage, len(sessions))
	for sid, msgs := range sessions {
		hmsgs := make([]historyMessage, len(msgs))
		for i, m := range msgs {
			hmsgs[i] = toHistoryMsg(m)
		}
		dto[sid] = hmsgs
	}
	return json.Marshal(dto)
}

func Unmarshal(data []byte) (map[string][]*schema.Message, error) {
	var dto map[string][]historyMessage
	if err := json.Unmarshal(data, &dto); err != nil {
		return nil, err
	}
	sessions := make(map[string][]*schema.Message, len(dto))
	for sid, hmsgs := range dto {
		msgs := make([]*schema.Message, len(hmsgs))
		for i, hm := range hmsgs {
			msgs[i] = fromHistoryMsg(hm)
		}
		sessions[sid] = msgs
	}
	return sessions, nil
}
