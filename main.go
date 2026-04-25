package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/attiasas/deus-ex-machina/agent"
	"github.com/attiasas/deus-ex-machina/provider"
	"github.com/attiasas/deus-ex-machina/tools"
)

// Injected at build time via -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

const usage = `deus — a free, open-source AI coding agent (default: Qwen2.5-Coder-7B, runs 100% locally)

Usage:
  deus [flags] [prompt]
  deus version

If prompt is omitted, reads from stdin.

Flags:
`

func main() {
	var (
		providerName = flag.String("provider", "local", "Provider: local | huggingface | ollama | anthropic | openai | gemini")
		model        = flag.String("model", "", "Model: HF repo ID, local .gguf path, or provider model name")
		systemPrompt = flag.String("system", "", "Override system prompt (default: built-in coding assistant prompt)")
		maxIter      = flag.Int("max-iter", 20, "Max agent loop iterations")
		baseURL      = flag.String("base-url", "", "API base URL override (for openai / ollama)")
		noConfirm    = flag.Bool("no-confirm", false, "Skip shell command confirmation prompts (unsafe)")
		verbose      = flag.Bool("v", false, "Verbose: print tool outputs")
		// local provider options
		localFilename = flag.String("local-filename", "", "GGUF filename pattern within HF repo (e.g. *q4_k_m.gguf)")
		localPort     = flag.Int("local-port", 0, "llama-server port (default 8765)")
		localGPU      = flag.Int("local-gpu", 0, "GPU layers for llama-server (-1 = all, 0 = CPU-only)")
		localCtx      = flag.Int("local-ctx", 0, "Context size for llama-server (default 8192)")
	)

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Env vars:
  HF_TOKEN           HuggingFace token (free — needed for huggingface provider, optional for local)
  ANTHROPIC_API_KEY  (for -provider anthropic)
  OPENAI_API_KEY     (for -provider openai)
  GEMINI_API_KEY     (for -provider gemini)

Examples:
  deus "Refactor main.go to use structured logging"
  deus -model Qwen/Qwen2.5-Coder-7B-Instruct-GGUF -local-filename "*q4_k_m.gguf" "write tests"
  deus -model /path/to/model.gguf "fix the bug"
  deus -provider ollama -model llama3.2 "write tests for agent.go"
  deus -provider anthropic "fix the bug in tools/shell.go"
  echo "what does main.go do?" | deus`)
	}
	flag.Parse()

	if flag.NArg() == 1 && flag.Arg(0) == "version" {
		fmt.Println(version)
		return
	}

	prompt, err := collectPrompt()
	if err != nil {
		flag.Usage()
		handleError(err)
	}

	p, err := setupProvider(provider.ProviderParam{
		Name:          *providerName,
		Model:         *model,
		BaseURL:       *baseURL,
		APIKey:        resolveAPIKey(*providerName),
		LocalFilename: *localFilename,
		LocalPort:     *localPort,
		LocalGPU:      *localGPU,
		LocalCtx:      *localCtx,
	}, *verbose)
	if err != nil {
		handleError(err)
	}

	a := &agent.Agent{
		Provider:             p,
		Registry:             tools.SetupToolRegistry(*noConfirm),
		MaxIter:              *maxIter,
		Verbose:              *verbose,
		SystemPromptTemplate: *systemPrompt, // empty = use default
	}

	if err := a.Run(context.Background(), prompt); err != nil {
		handleError(err)
	}
}

func collectPrompt() (string, error) {
	var prompt string
	if flag.NArg() > 0 {
		prompt = strings.Join(flag.Args(), " ")
	} else {
		scanner := bufio.NewScanner(os.Stdin)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		prompt = strings.Join(lines, "\n")
	}
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("no prompt provided")
	}
	return prompt, nil
}

func setupProvider(params provider.ProviderParam, verbose bool) (provider.Provider, error) {
	localCfg := provider.LocalConfig{
		HFFilename: params.LocalFilename,
		HFToken:    os.Getenv("HF_TOKEN"),
		Port:       params.LocalPort,
		NGPULayers: params.LocalGPU,
		NCtx:       params.LocalCtx,
		Verbose:    verbose,
	}
	return provider.New(params.Name, params.Model, params.BaseURL, params.APIKey, localCfg)
}

func resolveAPIKey(providerName string) string {
	switch providerName {
	case "huggingface", "hf":
		return os.Getenv("HF_TOKEN")
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY")
	case "openai":
		return os.Getenv("OPENAI_API_KEY")
	case "gemini":
		return os.Getenv("GEMINI_API_KEY")
	}
	return ""
}

func handleError(err error) {
	fmt.Fprintf(os.Stderr, "deus: %v\n", err)
	os.Exit(1)
}
