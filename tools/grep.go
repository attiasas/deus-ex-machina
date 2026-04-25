package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type GrepTool struct{}

func (GrepTool) Name() string { return "grep" }
func (GrepTool) Description() string {
	return "Search files for lines matching a regex. Returns file:line:content for each match."
}
func (GrepTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern":   {"type": "string", "description": "Regular expression to search for"},
			"path":      {"type": "string", "description": "File or directory to search (default: .)"},
			"recursive": {"type": "boolean", "description": "Search subdirectories (default: true)"}
		},
		"required": ["pattern"]
	}`)
}

func (GrepTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Pattern   string `json:"pattern"`
		Path      string `json:"path"`
		Recursive *bool  `json:"recursive"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Path == "" {
		params.Path = "."
	}
	recursive := true
	if params.Recursive != nil {
		recursive = *params.Recursive
	}

	re, err := regexp.Compile(params.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}

	var results []string
	grepPath(params.Path, re, recursive, &results)

	if len(results) == 0 {
		return "no matches found", nil
	}
	const max = 200
	if len(results) > max {
		results = append(results[:max], fmt.Sprintf("... truncated at %d results", max))
	}
	return strings.Join(results, "\n"), nil
}

func grepPath(path string, re *regexp.Regexp, recursive bool, results *[]string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if !info.IsDir() {
		grepFile(path, re, results)
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		child := filepath.Join(path, e.Name())
		if e.IsDir() {
			if recursive {
				grepPath(child, re, recursive, results)
			}
		} else {
			grepFile(child, re, results)
		}
	}
}

func grepFile(path string, re *regexp.Regexp, results *[]string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		n++
		if re.MatchString(scanner.Text()) {
			*results = append(*results, fmt.Sprintf("%s:%d: %s", path, n, scanner.Text()))
		}
	}
}
