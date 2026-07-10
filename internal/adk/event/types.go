package event

type Type int

const (
	AgentStart Type = iota
	AgentEnd
	StepStart
	StepEnd
	ModelStart
	ModelEnd
	ToolStart
	ToolEnd
	Error
)

type Event struct {
	Type      Type
	SessionID string
	Data      any
}

type AgentStartData struct {
	SessionID string
	Model     string
	ToolNames []string
}

type AgentEndData struct {
	SessionID string
	Stats     string
	ToolCalls int
}

type StepStartData struct {
	Step int
}

type StepEndData struct {
	Step   int
	Tokens int
}

type ModelStartData struct {
	Messages int
}

type ModelEndData struct {
	Tokens int
	Error  error
}

type ToolStartData struct {
	ToolName  string
	Arguments string
}

type ToolEndData struct {
	ToolName string
	Result   string
	Duration string
	Error    error
}

type ErrorData struct {
	Error error
}
