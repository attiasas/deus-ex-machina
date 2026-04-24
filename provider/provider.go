package provider

import (
	"context"
	"fmt"
	"io"

	"github.com/attiasas/deus-ex-machina/agent"
)

// Provider is the interface every model backend implements.
// Tools are communicated via the system prompt, not the API — all messages
// are plain text. Streaming text is written to out in real-time; the returned
// Response contains the fully accumulated Content.
type Provider interface {
	Complete(ctx context.Context, messages []agent.Message, out io.Writer) (*agent.Response, error)
}

// LocalConfig holds extra options for the local provider.
type LocalConfig struct {
	HFFilename string
	HFToken    string
	Port       int
	NGPULayers int
	NCtx       int
}

// New creates a provider by name. model/baseURL/apiKey may be empty to use defaults.
func New(name, model, baseURL, apiKey string, localCfg ...LocalConfig) (Provider, error) {
	switch name {
	case "huggingface", "hf":
		return NewHuggingFace(apiKey, model), nil
	case "ollama":
		return NewOllama(model, baseURL), nil
	case "anthropic":
		return NewAnthropic(apiKey, model), nil
	case "openai":
		return NewOpenAICompat(apiKey, model, baseURL), nil
	case "gemini":
		return NewGemini(apiKey, model), nil
	case "local":
		var cfg LocalConfig
		if len(localCfg) > 0 {
			cfg = localCfg[0]
		}
		return NewLocal(model, cfg.HFFilename, cfg.HFToken, cfg.Port, cfg.NGPULayers, cfg.NCtx), nil
	default:
		return nil, fmt.Errorf("unknown provider %q — valid: local, huggingface, ollama, anthropic, openai, gemini", name)
	}
}
