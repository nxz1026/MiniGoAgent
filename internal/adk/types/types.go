package types

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

type Message struct {
	Role             Role
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	ToolCallID       string
	Name             string
	MultiContent     []map[string]any
}

type Request struct {
	Messages  []*Message
	SessionID string
	ToolNames []string
}

type Response struct {
	Messages []*Message
	Stats    string
}

type EventType int

const (
	EventText EventType = iota
	EventReasoning
	EventToolCall
	EventToolResult
	EventError
	EventDone
)

type Event struct {
	Type    EventType
	Content string
	ToolID  string
	Error   error
}
