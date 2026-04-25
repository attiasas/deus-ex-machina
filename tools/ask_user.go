package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type AskUser struct{}

func (AskUser) Name() string { return "ask_user" }
func (AskUser) Description() string {
	return "Ask the user a clarification question and wait for their response before continuing."
}
func (AskUser) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"question": {"type": "string", "description": "The question to ask the user"}
		},
		"required": ["question"]
	}`)
}

func (AskUser) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Question string `json:"question"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\n[deus asks] %s\nYour answer: ", params.Question)
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read answer: %w", err)
	}
	return strings.TrimSpace(answer), nil
}
