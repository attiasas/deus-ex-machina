package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// SystemPromptTemplate is the default prompt. {tool_list} is replaced at runtime.
const SystemPromptTemplate = `You are a coding assistant whose goal it is to help us solve coding tasks.
You have access to a series of tools you can execute. Here are the tools you can execute:

{tool_list}

When you want to use a tool, reply with exactly one line in the format: ` + "`tool: TOOL_NAME({JSON_ARGS})`" + ` and nothing else.
Use compact single-line JSON with double quotes. After receiving a tool_result(...) message, continue the task.
If no tool is needed, respond normally.

IMPORTANT: Do NOT describe or announce what you are about to do. Do NOT say "I will use..." or "Let me...".
When a tool is needed, output ONLY the tool line immediately. No explanation before or after.

Example of correct behavior:
User: what is in config.json?
Assistant: ` + "`tool: read_file({\"path\": \"config.json\"})`" + `
User: tool_result(read_file): {"debug": true}
Assistant: config.json has one setting: debug mode is enabled.

## Professional Objectivity

Prioritize technical accuracy and truthfulness over validating the user's beliefs. 
Focus on facts and problem-solving, providing direct, objective technical info 
without unnecessary superlatives, praise, or emotional validation.

- Honestly apply the same rigorous standards to all ideas
- Disagree when necessary, even if it's not what the user wants to hear

## Tone and Style

- Only use emojis if the user explicitly requests it
- Output will be displayed on a command line interface—keep responses short and concise
- Use Github-flavored markdown (CommonMark specification)
- NEVER create files unless absolutely necessary—prefer editing existing files

### Best Practices

1. NEVER propose changes to code you haven't read - Read and understand existing code first`

// Provider is the interface the agent uses to call a model.
type Provider interface {
	Complete(ctx context.Context, messages []Message, out io.Writer) (*Response, error)
}

// Tool is the interface for executable tools.
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// Registry holds available tools.
type Registry interface {
	Get(name string) (Tool, error)
	All() []Tool
}

// Agent runs the agentic loop.
type Agent struct {
	Provider             Provider
	Registry             Registry
	MaxIter              int
	Verbose              bool
	SystemPromptTemplate string // defaults to SystemPromptTemplate if empty
}

func (a *Agent) Run(ctx context.Context, userQuery string) error {
	tmpl := a.SystemPromptTemplate
	if tmpl == "" {
		tmpl = SystemPromptTemplate
	}
	systemPrompt := buildSystemPrompt(tmpl, a.Registry.All())

	history := []Message{
		{Role: RoleSystem, Content: systemPrompt},
		{Role: RoleUser, Content: userQuery},
	}

	maxIter := a.MaxIter
	if maxIter <= 0 {
		maxIter = 20
	}

	for i := 0; i < maxIter; i++ {
		resp, err := a.Provider.Complete(ctx, history, os.Stdout)
		if err != nil {
			return err
		}
		history = append(history, Message{Role: RoleAssistant, Content: resp.Content})

		tc, ok := parseToolCall(resp.Content)
		if !ok {
			fmt.Println()
			break
		}

		fmt.Println() // newline after streamed text
		if a.Verbose {
			fmt.Fprintf(os.Stderr, "\u2192 %s %s\n", tc.Name, string(tc.Input))
		}

		tool, err := a.Registry.Get(tc.Name)
		if err != nil {
			if a.Verbose {
				fmt.Fprintf(os.Stderr, "\u2717 %s\n", err)
			}
			history = append(history, Message{
				Role:    RoleUser,
				Content: fmt.Sprintf("tool_result(%s): error: %s", tc.Name, err),
			})
			continue
		}

		result, err := tool.Execute(ctx, tc.Input)
		if err != nil {
			if a.Verbose {
				fmt.Fprintf(os.Stderr, "\u2717 error: %s\n", err)
			}
			history = append(history, Message{
				Role:    RoleUser,
				Content: fmt.Sprintf("tool_result(%s): error: %s", tc.Name, err),
			})
			continue
		}

		if a.Verbose {
			fmt.Fprintf(os.Stderr, "\u2713 done\n")
			fmt.Fprintln(os.Stderr, result)
		}
		history = append(history, Message{
			Role:    RoleUser,
			Content: fmt.Sprintf("tool_result(%s): %s", tc.Name, result),
		})
	}

	return nil
}

// buildSystemPrompt replaces {tool_list} in the template with a human-readable
// list of available tools derived from their Name, Description, and InputSchema.
func buildSystemPrompt(template string, tools []Tool) string {
	lines := make([]string, 0, len(tools))
	for _, t := range tools {
		argStr := schemaToArgStr(t.InputSchema())
		lines = append(lines, fmt.Sprintf("- %s(%s): %s", t.Name(), argStr, t.Description()))
	}
	toolList := strings.Join(lines, "\n")
	return strings.ReplaceAll(template, "{tool_list}", toolList+"\n")
}

// schemaToArgStr converts a JSON Schema object into a compact argument hint like
// {"path": "string", "recursive": true} for display in the system prompt.
// Non-string types use unquoted example values so the model produces valid JSON.
func schemaToArgStr(schema json.RawMessage) string {
	var s struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &s); err != nil || len(s.Properties) == 0 {
		return "{}"
	}

	// Required params first, then optional
	seen := map[string]bool{}
	parts := make([]string, 0, len(s.Properties))
	for _, name := range s.Required {
		if prop, ok := s.Properties[name]; ok {
			parts = append(parts, fmt.Sprintf(`"%s": %s`, name, typeExample(prop.Type)))
			seen[name] = true
		}
	}
	for name, prop := range s.Properties {
		if !seen[name] {
			parts = append(parts, fmt.Sprintf(`"%s": %s`, name, typeExample(prop.Type)))
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// typeExample returns a JSON example value for a given JSON Schema type string.
func typeExample(t string) string {
	switch t {
	case "boolean":
		return "true"
	case "integer", "number":
		return "0"
	default: // "string" and unknown types
		return `"string"`
	}
}

// parseToolCall scans response text for `tool: NAME({...})` anywhere in any line.
// The model may prepend reasoning text or wrap the call in backticks — both are handled.
func parseToolCall(text string) (ParsedToolCall, bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.ToLower(line))

		// Find "tool: " anywhere in the line, not just at the start
		idx := strings.Index(line, "tool: ")
		if idx < 0 {
			continue
		}
		rest := line[idx+6:]                 // content after "tool: "
		rest = strings.TrimSuffix(rest, "`") // strip trailing backtick if present

		parenIdx := strings.Index(rest, "(")
		if parenIdx < 0 {
			continue
		}
		name := strings.TrimSpace(rest[:parenIdx])
		argsStr := rest[parenIdx+1:]
		if last := strings.LastIndex(argsStr, ")"); last >= 0 {
			argsStr = argsStr[:last]
		}
		var raw json.RawMessage
		if err := json.Unmarshal([]byte(argsStr), &raw); err != nil {
			continue
		}
		return ParsedToolCall{Name: name, Input: raw}, true
	}
	return ParsedToolCall{}, false
}
