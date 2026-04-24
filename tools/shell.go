package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Shell struct {
	NoConfirm bool
}

func (Shell) Name() string        { return "shell" }
func (Shell) Description() string { return "Execute a shell command and return its output." }
func (Shell) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"cmd": {"type": "string", "description": "Shell command to execute"}
		},
		"required": ["cmd"]
	}`)
}

func (s Shell) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if !s.NoConfirm {
		fmt.Fprintf(os.Stderr, "  run: %s\n  confirm [y/N]: ", params.Cmd)
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		if !strings.HasPrefix(strings.TrimSpace(strings.ToLower(answer)), "y") {
			return "", fmt.Errorf("user declined")
		}
	}

	var stdout, stderr bytes.Buffer
	c := exec.Command("sh", "-c", params.Cmd)
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()

	out := stdout.String()
	if stderr.Len() > 0 {
		out += "\nstderr: " + stderr.String()
	}
	if err != nil {
		return out, fmt.Errorf("exit %w: %s", err, stderr.String())
	}
	return out, nil
}
