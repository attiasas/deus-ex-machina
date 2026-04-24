package tools

import (
	"fmt"

	"github.com/attiasas/deus-ex-machina/agent"
)

// Registry holds all registered tools.
type Registry struct {
	tools map[string]agent.Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]agent.Tool)}
}

func (r *Registry) Register(t agent.Tool) {
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (agent.Tool, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return t, nil
}

func (r *Registry) All() []agent.Tool {
	out := make([]agent.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}
