package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Glob struct{}

func (Glob) Name() string { return "glob" }
func (Glob) Description() string {
	return "Find files matching a glob pattern (supports **). Returns matching paths."
}
func (Glob) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Glob pattern, e.g. **/*.go or src/**/*.ts"}
		},
		"required": ["pattern"]
	}`)
}

func (Glob) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	matches, err := globPattern(params.Pattern)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "no files matched", nil
	}
	return strings.Join(matches, "\n"), nil
}

func globPattern(pattern string) ([]string, error) {
	if !strings.Contains(pattern, "**") {
		return filepath.Glob(pattern)
	}

	idx := strings.Index(pattern, "**")
	prefix := pattern[:idx]
	suffix := strings.TrimPrefix(pattern[idx+2:], string(filepath.Separator))
	suffix = strings.TrimPrefix(suffix, "/")

	base := "."
	if prefix != "" {
		base = filepath.Clean(prefix)
	}

	var matches []string
	_ = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Skip hidden paths
		for _, part := range strings.Split(filepath.ToSlash(path), "/") {
			if strings.HasPrefix(part, ".") {
				return nil
			}
		}
		if suffix == "" {
			matches = append(matches, path)
			return nil
		}
		if ok, _ := filepath.Match(suffix, filepath.Base(path)); ok {
			matches = append(matches, path)
		}
		return nil
	})
	return matches, nil
}
