# Contributing to deus ex machina

## Prerequisites

- Go 1.22 or later
- `llama-server` in PATH for testing the local provider (optional for other providers)

---

## Build

```bash
git clone https://github.com/attiasas/deus-ex-machina
cd deus-ex-machina
go build -o deus .
```

Zero external Go dependencies — pure stdlib. `go build` should just work.

## Run

```bash
./deus "your prompt here"
./deus --help
```

## Test

```bash
go test ./...
go vet ./...
```

Tests are minimal by design — the main validation is running the binary against a real provider. When adding a new provider or tool, add at least one unit test for the core serialization/parsing logic.

---

## Project Structure

```
deus-ex-machina/
├── main.go               # CLI: flag parsing, wires provider + tools + agent
├── agent/
│   ├── message.go        # Shared types: Message, ToolCall, ToolDef, Response, Role
│   └── agent.go          # Agentic loop + Provider/Registry/Tool interfaces
├── provider/
│   ├── provider.go       # Provider interface, LocalConfig, factory (New)
│   ├── openaicompat.go   # Streaming SSE core shared by HF, Ollama, OpenAI
│   ├── huggingface.go    # HuggingFace Inference API (wraps openaicompat)
│   ├── local.go          # Local GGUF: HF download + llama-server subprocess
│   ├── ollama.go         # Ollama: health check + auto-pull + openaicompat
│   ├── anthropic.go      # Anthropic Messages API with streaming SSE
│   └── gemini.go         # Google Gemini with streaming SSE
└── tools/
    ├── tool.go           # Registry (holds agent.Tool values)
    ├── readfile.go       # read_file tool
    ├── writefile.go      # write_file tool
    └── shell.go          # shell tool (y/N confirmation)
```

**Key design rule:** `agent/` owns all shared types (`Message`, `ToolCall`, `ToolDef`, `Response`, `Provider`, `Tool`, `Registry` interfaces). Both `provider/` and `tools/` import `agent/`. `agent/` imports neither.

---

## Adding a New Provider

1. Create `provider/myprovider.go` with a struct that implements `provider.Provider`:

```go
package provider

import (
    "context"
    "io"
    "github.com/attiasas/deus-ex-machina/agent"
)

type myProvider struct {
    apiKey string
    model  string
}

func NewMyProvider(apiKey, model string) Provider {
    // set defaults for empty fields
    return &myProvider{apiKey: apiKey, model: model}
}

func (p *myProvider) Complete(
    ctx context.Context,
    messages []agent.Message,
    tools []agent.ToolDef,
    out io.Writer,
) (*agent.Response, error) {
    // 1. Build the HTTP request body from messages + tools
    // 2. POST to the API with streaming enabled
    // 3. Parse the SSE/chunked response, write text chunks to out, accumulate tool calls
    // 4. Return a *agent.Response with Content + ToolCalls + StopReason
}
```

If the new API is OpenAI-compatible (common), just wrap `openAICompat`:

```go
func NewMyProvider(apiKey, model string) Provider {
    return NewOpenAICompat(apiKey, model, "https://api.myprovider.com")
}
```

2. Register it in `provider/provider.go` inside `New()`:

```go
case "myprovider":
    return NewMyProvider(apiKey, model), nil
```

3. Add it to the `-provider` flag description in `main.go`.

4. If it needs an API key, add a case in `resolveAPIKey()` in `main.go`:

```go
case "myprovider":
    return os.Getenv("MYPROVIDER_API_KEY")
```

### Implementing streaming SSE

All providers stream. Look at `openaicompat.go` for the OpenAI pattern or `anthropic.go` for a provider with a different SSE event format. The key contract:

- Write text tokens to `out` as they arrive (`fmt.Fprint(out, chunk)`)
- Accumulate the full text in a `strings.Builder`
- Collect tool calls across multiple delta events
- Return the fully accumulated `*agent.Response` at the end

### Message translation

Each provider has its own wire format. The agent passes `[]agent.Message` — you translate to the provider's format in `buildMessages` (or equivalent). Handle these three message shapes:

| `agent.Message` field set | Wire format |
|---|---|
| `Role=system, Content=...` | Provider system prompt (usually a top-level field, not in messages array) |
| `ToolCalls` populated | Assistant turn that requested tools |
| `ToolResults` populated | User turn returning tool outputs |

---

## Adding a New Tool

1. Create `tools/mytool.go`. Implement the `agent.Tool` interface:

```go
package tools

import (
    "context"
    "encoding/json"
)

type MyTool struct{}

func (MyTool) Name() string        { return "my_tool" }
func (MyTool) Description() string { return "One sentence describing what this tool does." }
func (MyTool) InputSchema() json.RawMessage {
    return json.RawMessage(`{
        "type": "object",
        "properties": {
            "param": {"type": "string", "description": "What this param does"}
        },
        "required": ["param"]
    }`)
}

func (MyTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
    var params struct {
        Param string `json:"param"`
    }
    if err := json.Unmarshal(input, &params); err != nil {
        return "", err
    }
    // do the work, return a string result the model can read
    return "result", nil
}
```

The `InputSchema()` is a JSON Schema object. The model uses it to know what arguments to pass.  
`Execute` returns a plain string — keep it concise; the model reads it as context.

2. Register the tool in `main.go`:

```go
reg.Register(tools.MyTool{})
```

---

## Code Conventions

- **No comments that explain what the code does** — names should do that. Add a comment only when the *why* is non-obvious (a workaround, a hidden constraint, a spec quirk).
- **No external dependencies** — stdlib only. If you think you need a library, reconsider.
- **Error messages start with `deus:`** when surfaced to the user (see existing providers for examples).
- **Streaming is mandatory** for all providers — write to `out` as tokens arrive. Never buffer the whole response.
- **Tool results are strings** — return plain text from `Execute`. If the data is structured, format it readably (e.g., JSON pretty-print or a simple table). Avoid binary data.
- **Confirmation for destructive tools** — any tool that modifies state outside the working tree (network calls, system changes) should follow the `shell` tool's `[y/N]` pattern unless `-no-confirm` is set.
