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
// Tools are in the system prompt — no function-calling fields are used.
type openAICompat struct {
	baseURL string
	apiKey  string
	model   string
}

func NewOpenAICompat(apiKey, model, baseURL string) Provider {
	return &openAICompat{baseURL: baseURL, apiKey: apiKey, model: model}
}

func (p *openAICompat) Complete(ctx context.Context, messages []agent.Message, out io.Writer) (*agent.Response, error) {
	msgs := make([]map[string]any, len(messages))
	for i, m := range messages {
		msgs[i] = map[string]any{"role": string(m.Role), "content": m.Content}
	}

	body, _ := json.Marshal(map[string]any{
		"model":    p.model,
		"messages": msgs,
		"stream":   true,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
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
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("provider error %d: %s", resp.StatusCode, string(b))
	}

	return parseOpenAIStream(resp.Body, out)
}

func parseOpenAIStream(body io.Reader, out io.Writer) (*agent.Response, error) {
	var buf strings.Builder
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
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		for _, c := range event.Choices {
			if c.FinishReason != "" {
				stopReason = c.FinishReason
			}
			if c.Delta.Content != "" {
				buf.WriteString(c.Delta.Content)
				fmt.Fprint(out, c.Delta.Content)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return &agent.Response{Content: buf.String(), StopReason: stopReason}, nil
}
