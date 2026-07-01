package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
)

// Tool is the execution contract used by the MCP execution runtime.
type Tool interface {
	Name() string
	Description() string
	InputSchema() any
	OutputSchema() any
	Execute(ctx context.Context, input json.RawMessage) (any, error)
}

// Registry owns the MCP runtime tool set. It is intentionally separate from the
// application registry so runtime capabilities remain explicitly scoped.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) error {
	if r == nil {
		return errors.New("nil registry")
	}
	if t == nil {
		return errors.New("nil tool")
	}
	name := strings.TrimSpace(t.Name())
	if name == "" {
		return errors.New("tool name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		return errors.New("tool already registered: " + name)
	}
	r.tools[name] = t
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[strings.TrimSpace(name)]
	return t, ok
}

func (r *Registry) List() []Tool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	r.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}
