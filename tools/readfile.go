package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// ReadFile implements agent.Tool.
type ReadFile struct{}

func (ReadFile) Name() string        { return "read_file" }
func (ReadFile) Description() string { return "Read the contents of a file at the given path." }
func (ReadFile) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to the file"}},"required":["path"]}`)
}

func (ReadFile) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	data, err := os.ReadFile(params.Path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
