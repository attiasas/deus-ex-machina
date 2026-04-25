# deus ex machina

A free, open-source AI coding agent that runs locally by default. No API keys required.

**Default:** downloads [Qwen2.5-7B-Instruct](https://huggingface.co/Qwen/Qwen2.5-7B-Instruct-GGUF) (~4.7 GB, Apache-2.0) and runs it on your machine. This model has the best tool-calling format compliance among 7B local models. Cloud providers (Anthropic, OpenAI, Gemini, HuggingFace) are optional.

The agent loops autonomously: it reasons, picks tools, acts, observes the results, and repeats until the task is done.

---

## Quick Start

### Prerequisites

**For local inference (default):** install [llama.cpp](https://github.com/ggerganov/llama.cpp) and make `llama-server` available in your PATH.

```bash
# macOS (Homebrew)
brew install llama.cpp

# Build from source
git clone https://github.com/ggerganov/llama.cpp && cd llama.cpp
cmake -B build && cmake --build build --target llama-server -j
```

**For cloud providers:** set the relevant API key env var (see [Providers](#providers)).

### Install

```bash
git clone https://github.com/attiasas/deus-ex-machina
cd deus-ex-machina
go build -o deus .
```

Or install directly:

```bash
go install github.com/attiasas/deus-ex-machina@latest
```

### First Run

```bash
# Downloads Qwen2.5-Coder-7B on first use (~4.7 GB), then runs the agent
deus "Explain what main.go does and suggest improvements"
```

The model is cached at `~/.cache/deus-ex-machina/models/` and reused on subsequent runs.

---

## Usage

```
deus [flags] [prompt]
```

If `prompt` is omitted, `deus` reads from stdin — useful for piping:

```bash
cat main.go | deus "review this file for bugs"
git diff HEAD~1 | deus "write a commit message for these changes"
```

---

## Providers

| Provider | Flag | Model default | Requires |
|---|---|---|---|
| **local** *(default)* | `-provider local` | `Qwen/Qwen2.5-7B-Instruct-GGUF` | `llama-server` in PATH |
| huggingface | `-provider huggingface` | `Qwen/Qwen2.5-Coder-7B-Instruct` | `HF_TOKEN` env var |
| ollama | `-provider ollama` | `qwen2.5-coder:7b` | Ollama running (`ollama serve`) |
| anthropic | `-provider anthropic` | `claude-sonnet-4-6` | `ANTHROPIC_API_KEY` |
| openai | `-provider openai` | `gpt-4o` | `OPENAI_API_KEY` |
| gemini | `-provider gemini` | `gemini-2.0-flash` | `GEMINI_API_KEY` |

### Local provider

Downloads GGUF models from HuggingFace and runs them via `llama-server`. Models are cached at `~/.cache/deus-ex-machina/models/`.

```bash
# Default: Qwen2.5-Coder-7B (best open-source coding model, ~4.7 GB)
deus "refactor agent.go to use structured logging"

# Different model from HuggingFace
deus -model bartowski/Qwen2.5-Coder-32B-Instruct-GGUF "analyze this codebase"

# Specific quantization
deus -model Qwen/Qwen2.5-Coder-7B-Instruct-GGUF -local-filename "*q8_0.gguf" "explain the streaming logic"

# Already-downloaded .gguf file
deus -model /path/to/model.gguf "write tests for tools/shell.go"

# Control hardware
deus -local-gpu 35 -local-ctx 32768 "refactor the provider package"
```

### HuggingFace Inference API

Uses HF's hosted free inference API — no local install needed, but requires an internet connection and a free HF token.

```bash
export HF_TOKEN=hf_...   # free at https://huggingface.co/settings/tokens
deus -provider huggingface "explain this codebase"
deus -provider huggingface -model mistralai/Mistral-7B-Instruct-v0.3 "fix the bug"
```

### Ollama

Connects to a local [Ollama](https://ollama.com) instance. Auto-pulls the model if not yet downloaded.

```bash
deus -provider ollama "write unit tests"
deus -provider ollama -model llama3.2 "what does main.go do?"
# HuggingFace model IDs also work via Ollama's hf.co/ prefix:
deus -provider ollama -model hf.co/Qwen/Qwen2.5-Coder-7B-Instruct-GGUF "refactor"
```

### Cloud providers

```bash
ANTHROPIC_API_KEY=... deus -provider anthropic "rewrite agent.go with better error handling"
OPENAI_API_KEY=...    deus -provider openai -model gpt-4o "review main.go"
GEMINI_API_KEY=...    deus -provider gemini "add docstrings to all exported functions"
```

---

## Tools

| Tool | What it does |
|---|---|
| `read_file` | Read a file from disk |
| `write_file` | Write content to a file (creates parent dirs) |
| `edit_file` | In-place string replacement in a file (first occurrence) |
| `glob` | Find files by path pattern — supports `**` (e.g. `**/*.go`) |
| `grep` | Search files with a regex — returns `file:line: content` matches |
| `web_fetch` | Fetch a URL and return its text content |
| `web_search` | Search the web via DuckDuckGo (no API key required) |
| `shell` | Run a shell command (prompts `[y/N]` before executing) |

The shell tool asks for confirmation before running each command. Pass `-no-confirm` to skip (use with care).

---

## All Flags

```
  -provider string      Provider to use (default "local")
  -model string         Model: HF repo ID, local .gguf path, or provider model name
  -system string        System prompt
  -max-iter int         Max agent loop iterations (default 20)
  -base-url string      API base URL override (for openai / ollama)
  -no-confirm           Skip shell command confirmation prompts (unsafe)
  -v                    Verbose: show tool calls, results, and provider status messages

Local provider flags:
  -local-filename string   GGUF filename pattern within HF repo (e.g. *q4_k_m.gguf)
  -local-port int          llama-server port (default 8765)
  -local-gpu int           GPU layers (-1 = all layers on GPU, 0 = auto)
  -local-ctx int           Context window size (default 32768)
```

## Environment Variables

```
HF_TOKEN           HuggingFace token — needed for huggingface provider, optional for local (private repos)
ANTHROPIC_API_KEY  Anthropic API key
OPENAI_API_KEY     OpenAI / compatible API key
GEMINI_API_KEY     Google Gemini API key
```

---

## Why "deus ex machina"?

A deus ex machina is a plot device where an unsolvable problem is suddenly resolved by an unexpected agent. That's the idea: hand `deus` a messy task and it figures it out.

---

## License

Apache-2.0
