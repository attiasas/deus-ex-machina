package provider

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/attiasas/deus-ex-machina/agent"
)

const hfBaseURL = "https://api-inference.huggingface.co"
const hfDefaultModel = "Qwen/Qwen2.5-Coder-7B-Instruct"

type huggingFace struct {
	inner *openAICompat
}

func NewHuggingFace(apiKey, model string) Provider {
	if apiKey == "" {
		apiKey = os.Getenv("HF_TOKEN")
	}
	if model == "" {
		model = hfDefaultModel
	}
	return &huggingFace{inner: &openAICompat{baseURL: hfBaseURL, apiKey: apiKey, model: model}}
}

func (h *huggingFace) Complete(ctx context.Context, messages []agent.Message, out io.Writer) (*agent.Response, error) {
	if h.inner.apiKey == "" {
		return nil, fmt.Errorf("deus: HF_TOKEN is not set — get a free token at https://huggingface.co/settings/tokens")
	}
	return h.inner.Complete(ctx, messages, out)
}
