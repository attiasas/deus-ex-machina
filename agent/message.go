package agent

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ToolCall is a request from the model to invoke a tool.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult is the harness response to a ToolCall.
type ToolResult struct {
	CallID  string
	Content string
	IsError bool
}

// Message is a single turn in the conversation.
type Message struct {
	Role        Role
	Content     string
	ToolCalls   []ToolCall   // populated when role==assistant and model wants tools
	ToolResults []ToolResult // populated for tool-result turns
}

// ToolDef describes a tool the model can call.
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Response is what a provider returns per turn.
type Response struct {
	Content    string
	ToolCalls  []ToolCall
	StopReason string
}
