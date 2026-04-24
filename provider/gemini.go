package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/attiasas/deus-ex-machina/agent"
)

const geminiDefaultModel = "gemini-2.0-flash"
const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta/models"

type geminiProvider struct {
	apiKey string
	model  string
}

func NewGemini(apiKey, model string) Provider {
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if model == "" {
		model = geminiDefaultModel
	}
	return &geminiProvider{apiKey: apiKey, model: model}
}

func (g *geminiProvider) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, out io.Writer) (*agent.Response, error) {
	if g.apiKey == "" {
		return nil, fmt.Errorf("deus: GEMINI_API_KEY is not set")
	}

	system, contents := g.buildContents(messages)
	reqBody := map[string]any{"contents": contents}
	if system != "" {
		reqBody["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": system}},
		}
	}
	if len(tools) > 0 {
		decls := make([]map[string]any, len(tools))
		for i, t := range tools {
			decls[i] = map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			}
		}
		reqBody["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}

	data, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/%s:streamGenerateContent?key=%s&alt=sse", geminiBaseURL, g.model, g.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini error %d: %s", resp.StatusCode, string(body))
	}

	return g.parseStream(resp.Body, out)
}

func (g *geminiProvider) buildContents(messages []agent.Message) (system string, contents []map[string]any) {
	for _, m := range messages {
		if m.Role == agent.RoleSystem {
			system = m.Content
			continue
		}
		switch {
		case len(m.ToolResults) > 0:
			parts := make([]map[string]any, len(m.ToolResults))
			for i, tr := range m.ToolResults {
				parts[i] = map[string]any{
					"functionResponse": map[string]any{
						"name":     tr.CallID,
						"response": map[string]any{"output": tr.Content},
					},
				}
			}
			contents = append(contents, map[string]any{"role": "user", "parts": parts})
		case len(m.ToolCalls) > 0:
			parts := []map[string]any{}
			if m.Content != "" {
				parts = append(parts, map[string]any{"text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var argsObj any
				_ = json.Unmarshal(tc.Input, &argsObj)
				parts = append(parts, map[string]any{
					"functionCall": map[string]any{"name": tc.Name, "args": argsObj},
				})
			}
			contents = append(contents, map[string]any{"role": "model", "parts": parts})
		default:
			role := "user"
			if m.Role == agent.RoleAssistant {
				role = "model"
			}
			contents = append(contents, map[string]any{
				"role":  role,
				"parts": []map[string]any{{"text": m.Content}},
			})
		}
	}
	return
}

func (g *geminiProvider) parseStream(body io.Reader, out io.Writer) (*agent.Response, error) {
	var textBuf strings.Builder
	var toolCalls []agent.ToolCall
	var stopReason string

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]

		var event struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text         string `json:"text"`
						FunctionCall *struct {
							Name string         `json:"name"`
							Args map[string]any `json:"args"`
						} `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		for _, candidate := range event.Candidates {
			if candidate.FinishReason != "" {
				stopReason = candidate.FinishReason
			}
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					textBuf.WriteString(part.Text)
					fmt.Fprint(out, part.Text)
				}
				if part.FunctionCall != nil {
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					toolCalls = append(toolCalls, agent.ToolCall{
						ID:    part.FunctionCall.Name,
						Name:  part.FunctionCall.Name,
						Input: json.RawMessage(argsJSON),
					})
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return &agent.Response{
		Content:    textBuf.String(),
		ToolCalls:  toolCalls,
		StopReason: stopReason,
	}, nil
}
