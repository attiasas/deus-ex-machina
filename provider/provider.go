package provider

import (
	"context"
	"fmt"
	"io"

	"github.com/attiasas/deus-ex-machina/agent"
)

// Provider is the interface every model backend implements.
// Streaming text is written to out in real-time; the returned
// Response contains the fully accumulated Content.
type Provider interface {
	Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, out io.Writer) (*agent.Response, error)
}

// LocalConfig holds extra options for the local provider.
type LocalConfig struct {
	HFFilename string // GGUF filename pattern within a HF repo
	HFToken    string // HuggingFace token for private models
	Port       int    // llama-server port (default 8765)
	NGPULayers int    // GPU layers passed to llama-server (-1 = auto)
	NCtx       int    // Context size (0 = 2048)
}

// New creates a provider by name. model/baseURL/apiKey may be empty to use defaults.
// localCfg is only used when name == "local".
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
		return nil, fmt.Errorf("unknown provider %q — valid: huggingface, ollama, local, anthropic, openai, gemini", name)
	}
}
