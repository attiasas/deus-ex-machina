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

func (g *geminiProvider) Complete(ctx context.Context, messages []agent.Message, out io.Writer) (*agent.Response, error) {
	if g.apiKey == "" {
		return nil, fmt.Errorf("deus: GEMINI_API_KEY is not set")
	}

	// Gemini takes system prompt as a top-level field; roles are "user"/"model".
	var system string
	var contents []map[string]any
	for _, m := range messages {
		if m.Role == agent.RoleSystem {
			system = m.Content
			continue
		}
		role := "user"
		if m.Role == agent.RoleAssistant {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]any{{"text": m.Content}},
		})
	}

	reqBody := map[string]any{"contents": contents}
	if system != "" {
		reqBody["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": system}},
		}
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

	return parseGeminiStream(resp.Body, out)
}

func parseGeminiStream(body io.Reader, out io.Writer) (*agent.Response, error) {
	var buf strings.Builder
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
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		for _, c := range event.Candidates {
			if c.FinishReason != "" {
				stopReason = c.FinishReason
			}
			for _, p := range c.Content.Parts {
				if p.Text != "" {
					buf.WriteString(p.Text)
					fmt.Fprint(out, p.Text)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return &agent.Response{Content: buf.String(), StopReason: stopReason}, nil
}
