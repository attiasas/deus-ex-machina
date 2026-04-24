package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type WriteFile struct{}

func (WriteFile) Name() string        { return "write_file" }
func (WriteFile) Description() string { return "Write content to a file at the given path." }
func (WriteFile) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path":    {"type": "string", "description": "Path to the file to write"},
			"content": {"type": "string", "description": "Content to write to the file"}
		},
		"required": ["path", "content"]
	}`)
}

func (WriteFile) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(params.Path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(params.Path, []byte(params.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(params.Content), params.Path), nil
}
