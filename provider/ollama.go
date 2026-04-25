package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/attiasas/deus-ex-machina/agent"
)

const ollamaDefaultBaseURL = "http://localhost:11434"
const ollamaDefaultModel = "qwen2.5-coder:7b"

type ollama struct {
	inner   *openAICompat
	baseURL string
	model   string
	log     io.Writer
}

func NewOllama(model, baseURL string, verbose bool) Provider {
	if model == "" {
		model = ollamaDefaultModel
	}
	if baseURL == "" {
		baseURL = ollamaDefaultBaseURL
	}
	var logW io.Writer = io.Discard
	if verbose {
		logW = os.Stderr
	}
	return &ollama{
		inner:   &openAICompat{baseURL: baseURL, apiKey: "ollama", model: model},
		baseURL: baseURL,
		model:   model,
		log:     logW,
	}
}

func (o *ollama) Complete(ctx context.Context, messages []agent.Message, out io.Writer) (*agent.Response, error) {
	if err := o.ensureHealthy(); err != nil {
		return nil, err
	}
	if err := o.ensureModel(ctx); err != nil {
		return nil, err
	}
	return o.inner.Complete(ctx, messages, out)
}

func (o *ollama) ensureHealthy() error {
	resp, err := http.Get(o.baseURL + "/api/tags")
	if err != nil {
		return fmt.Errorf("deus: Ollama is not running — install at https://ollama.com and run 'ollama serve'")
	}
	resp.Body.Close()
	return nil
}

func (o *ollama) ensureModel(ctx context.Context) error {
	resp, err := http.Get(o.baseURL + "/api/tags")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	for _, m := range result.Models {
		if m.Name == o.model || m.Name == o.model+":latest" {
			return nil
		}
	}

	fmt.Fprintf(o.log, "pulling model %s ...\n", o.model)
	body, _ := json.Marshal(map[string]any{"name": o.model, "stream": false})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/pull", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	pullResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to pull model %s: %w", o.model, err)
	}
	defer pullResp.Body.Close()
	io.Copy(io.Discard, pullResp.Body)
	fmt.Fprintf(o.log, "model %s ready\n", o.model)
	return nil
}
