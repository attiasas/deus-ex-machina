package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type mockTool struct {
	name, desc string
	schema     json.RawMessage
}

func (m mockTool) Name() string                                              { return m.name }
func (m mockTool) Description() string                                       { return m.desc }
func (m mockTool) InputSchema() json.RawMessage                              { return m.schema }
func (m mockTool) Execute(_ context.Context, _ json.RawMessage) (string, error) { return "", nil }

func assertJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("got invalid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatalf("want invalid JSON: %v", err)
	}
	ga, _ := json.Marshal(a)
	gb, _ := json.Marshal(b)
	if string(ga) != string(gb) {
		t.Errorf("JSON mismatch: got %s, want %s", ga, gb)
	}
}

func TestParseToolCall(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantOK    bool
		wantName  string
		wantInput string
	}{
		{
			name:      "clean line",
			input:     `tool: read_file({"path":"main.go"})`,
			wantOK:    true,
			wantName:  "read_file",
			wantInput: `{"path":"main.go"}`,
		},
		{
			name:     "inline preamble",
			input:    "I need to look at the files and their structure. `tool: read_file({\"path\": \"README.md\"})`",
			wantOK:   true,
			wantName: "read_file",
		},
		{
			name:     "backticks only",
			input:    "`tool: shell({\"cmd\":\"ls -la\"})`",
			wantOK:   true,
			wantName: "shell",
		},
		{
			name:     "tool on second line",
			input:    "Let me think about this.\ntool: write_file({\"path\":\"out.txt\",\"content\":\"hi\"})",
			wantOK:   true,
			wantName: "write_file",
		},
		{
			name:   "no tool line",
			input:  "Here is the answer: 42. No tools needed.",
			wantOK: false,
		},
		{
			name:   "malformed JSON",
			input:  "tool: bad({not valid json})",
			wantOK: false,
		},
		{
			name:   "missing parens",
			input:  "tool: read_file",
			wantOK: false,
		},
		{
			name:     "empty args",
			input:    "tool: list_files({})",
			wantOK:   true,
			wantName: "list_files",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseToolCall(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got.Name != tc.wantName {
				t.Errorf("name = %q, want %q", got.Name, tc.wantName)
			}
			if tc.wantInput != "" {
				assertJSON(t, got.Input, tc.wantInput)
			}
		})
	}
}

func TestSchemaToArgStr(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		check  func(t *testing.T, got string)
	}{
		{
			name:   "single required param",
			schema: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
			check: func(t *testing.T, got string) {
				if got != `{"path": "string"}` {
					t.Errorf("got %q", got)
				}
			},
		},
		{
			name:   "two required params",
			schema: `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`,
			check: func(t *testing.T, got string) {
				if !strings.Contains(got, `"path": "string"`) || !strings.Contains(got, `"content": "string"`) {
					t.Errorf("missing expected params, got %q", got)
				}
			},
		},
		{
			name:   "required before optional",
			schema: `{"type":"object","properties":{"path":{"type":"string"},"encoding":{"type":"string"}},"required":["path"]}`,
			check: func(t *testing.T, got string) {
				pathIdx := strings.Index(got, `"path"`)
				encIdx := strings.Index(got, `"encoding"`)
				if pathIdx < 0 || encIdx < 0 {
					t.Fatalf("params missing, got %q", got)
				}
				if pathIdx > encIdx {
					t.Errorf("required 'path' should appear before optional 'encoding', got %q", got)
				}
			},
		},
		{
			name:   "boolean param emits unquoted true",
			schema: `{"type":"object","properties":{"recursive":{"type":"boolean"}},"required":["recursive"]}`,
			check: func(t *testing.T, got string) {
				if !strings.Contains(got, `"recursive": true`) {
					t.Errorf("boolean param should emit unquoted true, got %q", got)
				}
			},
		},
		{
			name:   "integer param emits unquoted zero",
			schema: `{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}`,
			check: func(t *testing.T, got string) {
				if !strings.Contains(got, `"count": 0`) {
					t.Errorf("integer param should emit unquoted 0, got %q", got)
				}
			},
		},
		{
			name:   "boolean hint produces valid JSON",
			schema: `{"type":"object","properties":{"recursive":{"type":"boolean"},"path":{"type":"string"}},"required":["path"]}`,
			check: func(t *testing.T, got string) {
				if err := json.Unmarshal([]byte(got), new(any)); err != nil {
					t.Errorf("hint is not valid JSON: %v — got %q", err, got)
				}
			},
		},
		{
			name:   "invalid schema",
			schema: `not json`,
			check: func(t *testing.T, got string) {
				if got != "{}" {
					t.Errorf("expected {} for invalid schema, got %q", got)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := schemaToArgStr(json.RawMessage(tc.schema))
			tc.check(t, got)
		})
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	readFileTool := mockTool{
		name:   "read_file",
		desc:   "Read a file.",
		schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
	}

	cases := []struct {
		name     string
		template string
		tools    []Tool
		check    func(t *testing.T, got string)
	}{
		{
			name:     "empty tools",
			template: "{tool_list}",
			tools:    []Tool{},
			check: func(t *testing.T, got string) {
				if strings.Contains(got, "{tool_list}") {
					t.Error("placeholder was not replaced")
				}
			},
		},
		{
			name:     "single tool",
			template: "TOOLS:\n{tool_list}END",
			tools:    []Tool{readFileTool},
			check: func(t *testing.T, got string) {
				if !strings.Contains(got, "- read_file") {
					t.Errorf("tool name not in output: %q", got)
				}
				if !strings.Contains(got, "Read a file.") {
					t.Errorf("description not in output: %q", got)
				}
				if !strings.Contains(got, `"path": "string"`) {
					t.Errorf("param hint not in output: %q", got)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildSystemPrompt(tc.template, tc.tools)
			tc.check(t, got)
		})
	}
}
