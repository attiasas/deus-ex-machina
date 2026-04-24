package agent

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single turn in the conversation.
// All tool communication is plain text — no structured tool_calls fields.
type Message struct {
	Role    Role
	Content string
}

// ToolDef describes a tool, used to build the system prompt tool list.
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Response is what a provider returns per turn.
type Response struct {
	Content    string
	StopReason string
}

// ParsedToolCall is the result of parsing a `tool: NAME({...})` line from a response.
type ParsedToolCall struct {
	Name  string
	Input json.RawMessage
}
