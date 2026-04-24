package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/attiasas/deus-ex-machina/agent"
)

// openAICompat implements the OpenAI chat completions API with SSE streaming.
// Used by HuggingFace, Ollama, and the openai provider.
type openAICompat struct {
	baseURL string
	apiKey  string
	model   string
}

func NewOpenAICompat(apiKey, model, baseURL string) Provider {
	return &openAICompat{baseURL: baseURL, apiKey: apiKey, model: model}
}

func (p *openAICompat) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, out io.Writer) (*agent.Response, error) {
	reqBody := p.buildRequest(messages, tools)
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("provider error %d: %s", resp.StatusCode, string(body))
	}

	return p.parseStream(resp.Body, out)
}

func (p *openAICompat) buildRequest(messages []agent.Message, tools []agent.ToolDef) map[string]any {
	msgs := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		switch {
		case len(m.ToolResults) > 0:
			for _, tr := range m.ToolResults {
				msgs = append(msgs, map[string]any{
					"role":         "tool",
					"tool_call_id": tr.CallID,
					"content":      tr.Content,
				})
			}
		case len(m.ToolCalls) > 0:
			tcs := make([]map[string]any, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				tcs[i] = map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": string(tc.Input),
					},
				}
			}
			msgs = append(msgs, map[string]any{
				"role":       "assistant",
				"content":    m.Content,
				"tool_calls": tcs,
			})
		default:
			msgs = append(msgs, map[string]any{
				"role":    string(m.Role),
				"content": m.Content,
			})
		}
	}

	req := map[string]any{
		"model":    p.model,
		"messages": msgs,
		"stream":   true,
	}
	if len(tools) > 0 {
		toolList := make([]map[string]any, len(tools))
		for i, t := range tools {
			toolList[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.InputSchema,
				},
			}
		}
		req["tools"] = toolList
	}
	return req
}

// streamToolCall accumulates a streaming tool call across multiple deltas.
type streamToolCall struct {
	id        string
	name      string
	arguments strings.Builder
}

func (p *openAICompat) parseStream(body io.Reader, out io.Writer) (*agent.Response, error) {
	var textBuf strings.Builder
	toolCalls := map[int]*streamToolCall{}
	stopReason := "stop"

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]
		if payload == "[DONE]" {
			break
		}

		var event struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		for _, choice := range event.Choices {
			if choice.FinishReason != "" {
				stopReason = choice.FinishReason
			}
			if choice.Delta.Content != "" {
				textBuf.WriteString(choice.Delta.Content)
				fmt.Fprint(out, choice.Delta.Content)
			}
			for _, tc := range choice.Delta.ToolCalls {
				if _, ok := toolCalls[tc.Index]; !ok {
					toolCalls[tc.Index] = &streamToolCall{}
				}
				stc := toolCalls[tc.Index]
				if tc.ID != "" {
					stc.id = tc.ID
				}
				if tc.Function.Name != "" {
					stc.name = tc.Function.Name
				}
				stc.arguments.WriteString(tc.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	result := &agent.Response{
		Content:    textBuf.String(),
		StopReason: stopReason,
	}

	for i := 0; i < len(toolCalls); i++ {
		stc := toolCalls[i]
		var raw json.RawMessage
		if err := json.Unmarshal([]byte(stc.arguments.String()), &raw); err != nil {
			raw = json.RawMessage("{}")
		}
		result.ToolCalls = append(result.ToolCalls, agent.ToolCall{
			ID:    stc.id,
			Name:  stc.name,
			Input: raw,
		})
	}
	return result, nil
}
