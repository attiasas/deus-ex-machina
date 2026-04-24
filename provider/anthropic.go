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

const anthropicDefaultModel = "claude-sonnet-4-6"

type anthropicProvider struct {
	apiKey string
	model  string
}

func NewAnthropic(apiKey, model string) Provider {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if model == "" {
		model = anthropicDefaultModel
	}
	return &anthropicProvider{apiKey: apiKey, model: model}
}

func (a *anthropicProvider) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, out io.Writer) (*agent.Response, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("deus: ANTHROPIC_API_KEY is not set")
	}

	system, msgs := a.buildMessages(messages)
	reqBody := map[string]any{
		"model":      a.model,
		"max_tokens": 4096,
		"stream":     true,
		"messages":   msgs,
	}
	if system != "" {
		reqBody["system"] = system
	}
	if len(tools) > 0 {
		toolList := make([]map[string]any, len(tools))
		for i, t := range tools {
			toolList[i] = map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.InputSchema,
			}
		}
		reqBody["tools"] = toolList
	}

	data, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic error %d: %s", resp.StatusCode, string(body))
	}

	return a.parseStream(resp.Body, out)
}

func (a *anthropicProvider) buildMessages(messages []agent.Message) (system string, msgs []map[string]any) {
	for _, m := range messages {
		if m.Role == agent.RoleSystem {
			system = m.Content
			continue
		}
		switch {
		case len(m.ToolResults) > 0:
			content := make([]map[string]any, len(m.ToolResults))
			for i, tr := range m.ToolResults {
				content[i] = map[string]any{
					"type":        "tool_result",
					"tool_use_id": tr.CallID,
					"content":     tr.Content,
				}
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": content})
		case len(m.ToolCalls) > 0:
			content := []map[string]any{}
			if m.Content != "" {
				content = append(content, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var inputObj any
				_ = json.Unmarshal(tc.Input, &inputObj)
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": inputObj,
				})
			}
			msgs = append(msgs, map[string]any{"role": "assistant", "content": content})
		default:
			msgs = append(msgs, map[string]any{"role": string(m.Role), "content": m.Content})
		}
	}
	return
}

func (a *anthropicProvider) parseStream(body io.Reader, out io.Writer) (*agent.Response, error) {
	type toolBlock struct {
		id   string
		name string
		json strings.Builder
	}

	var textBuf strings.Builder
	toolBlocks := map[int]*toolBlock{}
	var stopReason string

	scanner := bufio.NewScanner(body)
	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]

		switch eventType {
		case "content_block_start":
			var e struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if json.Unmarshal([]byte(payload), &e) == nil {
				if e.ContentBlock.Type == "tool_use" {
					toolBlocks[e.Index] = &toolBlock{id: e.ContentBlock.ID, name: e.ContentBlock.Name}
				}
			}

		case "content_block_delta":
			var e struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(payload), &e) == nil {
				switch e.Delta.Type {
				case "text_delta":
					textBuf.WriteString(e.Delta.Text)
					fmt.Fprint(out, e.Delta.Text)
				case "input_json_delta":
					if tb, ok := toolBlocks[e.Index]; ok {
						tb.json.WriteString(e.Delta.PartialJSON)
					}
				}
			}

		case "message_delta":
			var e struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(payload), &e) == nil && e.Delta.StopReason != "" {
				stopReason = e.Delta.StopReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	result := &agent.Response{Content: textBuf.String(), StopReason: stopReason}
	for i := 0; i < len(toolBlocks); i++ {
		tb := toolBlocks[i]
		var raw json.RawMessage
		if err := json.Unmarshal([]byte(tb.json.String()), &raw); err != nil {
			raw = json.RawMessage("{}")
		}
		result.ToolCalls = append(result.ToolCalls, agent.ToolCall{
			ID:    tb.id,
			Name:  tb.name,
			Input: raw,
		})
	}
	return result, nil
}
