package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Provider is the interface the agent uses to call a model.
type Provider interface {
	Complete(ctx context.Context, messages []Message, tools []ToolDef, out io.Writer) (*Response, error)
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
	Provider     Provider
	Registry     Registry
	MaxIter      int
	Verbose      bool
	SystemPrompt string
}

func (a *Agent) Run(ctx context.Context, userQuery string) error {
	history := []Message{
		{Role: RoleSystem, Content: a.SystemPrompt},
		{Role: RoleUser, Content: userQuery},
	}

	toolDefs := make([]ToolDef, 0)
	for _, t := range a.Registry.All() {
		toolDefs = append(toolDefs, ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}

	maxIter := a.MaxIter
	if maxIter <= 0 {
		maxIter = 20
	}

	for i := 0; i < maxIter; i++ {
		resp, err := a.Provider.Complete(ctx, history, toolDefs, os.Stdout)
		if err != nil {
			return err
		}

		history = append(history, Message{
			Role:      RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		if len(resp.ToolCalls) == 0 {
			fmt.Println()
			break
		}

		fmt.Println()

		var toolResults []ToolResult
		for _, tc := range resp.ToolCalls {
			compact, _ := json.Marshal(json.RawMessage(tc.Input))
			fmt.Fprintf(os.Stderr, "\u2192 %s %s\n", tc.Name, string(compact))

			tool, err := a.Registry.Get(tc.Name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\u2717 %s\n", err)
				toolResults = append(toolResults, ToolResult{CallID: tc.ID, Content: err.Error(), IsError: true})
				continue
			}

			result, err := tool.Execute(ctx, tc.Input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\u2717 error: %s\n", err)
				toolResults = append(toolResults, ToolResult{CallID: tc.ID, Content: err.Error(), IsError: true})
				continue
			}

			fmt.Fprintf(os.Stderr, "\u2713 done\n")
			if a.Verbose && result != "" {
				fmt.Fprintln(os.Stderr, result)
			}
			toolResults = append(toolResults, ToolResult{CallID: tc.ID, Content: result})
		}

		history = append(history, Message{Role: RoleUser, ToolResults: toolResults})
	}

	return nil
}
