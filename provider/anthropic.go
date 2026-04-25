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

func (a *anthropicProvider) Complete(ctx context.Context, messages []agent.Message, out io.Writer) (*agent.Response, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("deus: ANTHROPIC_API_KEY is not set")
	}

	// Anthropic takes system prompt as a top-level field, not in the messages array.
	var system string
	var msgs []map[string]any
	for _, m := range messages {
		if m.Role == agent.RoleSystem {
			system = m.Content
			continue
		}
		msgs = append(msgs, map[string]any{"role": string(m.Role), "content": m.Content})
	}

	reqBody := map[string]any{
		"model":      a.model,
		"max_tokens": 4096,
		"stream":     true,
		"messages":   msgs,
	}
	if system != "" {
		reqBody["system"] = system
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

	return parseAnthropicStream(resp.Body, out)
}

func parseAnthropicStream(body io.Reader, out io.Writer) (*agent.Response, error) {
	var buf strings.Builder
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
		case "content_block_delta":
			var e struct {
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(payload), &e) == nil && e.Delta.Type == "text_delta" {
				buf.WriteString(e.Delta.Text)
				fmt.Fprint(out, e.Delta.Text)
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
	return &agent.Response{Content: buf.String(), StopReason: stopReason}, nil
}
