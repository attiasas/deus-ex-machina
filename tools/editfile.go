package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type EditFile struct{}

func (EditFile) Name() string        { return "edit_file" }
func (EditFile) Description() string { return "Replace the first occurrence of old_string with new_string in a file." }
func (EditFile) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path":       {"type": "string", "description": "Path to the file"},
			"old_string": {"type": "string", "description": "Exact string to find"},
			"new_string": {"type": "string", "description": "Replacement string"}
		},
		"required": ["path", "old_string", "new_string"]
	}`)
}

func (EditFile) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	data, err := os.ReadFile(params.Path)
	if err != nil {
		return "", err
	}
	content := string(data)
	if !strings.Contains(content, params.OldString) {
		return "", fmt.Errorf("old_string not found in %s", params.Path)
	}
	updated := strings.Replace(content, params.OldString, params.NewString, 1)
	if err := os.WriteFile(params.Path, []byte(updated), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", params.Path), nil
}
