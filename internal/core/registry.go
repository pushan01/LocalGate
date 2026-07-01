package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type InvokeContext struct {
	TaskID          string
	WorkspaceID     string
	Requester       string
	ApprovalID      string
	Transport       string
	MCPSessionID    string
	SessionMode     string
	SessionIDSource string
	RequestID       string
	Authorized      bool
	Sink            *EventSink
}

type Tool interface {
	Name() string
	Description() string
	Schema() any
	Execute(ctx context.Context, ic InvokeContext, args map[string]any) (map[string]any, error)
}

type ToolMetadataProvider interface {
	ToolMetadata() map[string]any
}

func ToolMetadata(t Tool) map[string]any {
	if t == nil {
		return nil
	}
	p, ok := t.(ToolMetadataProvider)
	if !ok {
		return nil
	}
	meta := p.ToolMetadata()
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}

type Registry struct {
	mu     sync.RWMutex
	tools  map[string]Tool
	hidden map[string]struct{}
	sink   *EventSink
}

func NewRegistry(sink *EventSink) *Registry {
	return &Registry{
		tools:  make(map[string]Tool),
		hidden: make(map[string]struct{}),
		sink:   sink,
	}
}

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

func (r *Registry) Hide(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(name) == "" {
		return
	}
	r.hidden[name] = struct{}{}
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) GetVisible(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, hidden := r.hidden[name]; hidden {
		return nil, false
	}
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) IsVisible(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.tools[name]; !ok {
		return false
	}
	_, hidden := r.hidden[name]
	return !hidden
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for k := range r.tools {
		out = append(out, k)
	}
	return out
}

func (r *Registry) ListVisible() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for k := range r.tools {
		if _, hidden := r.hidden[k]; hidden {
			continue
		}
		out = append(out, k)
	}
	return out
}

var ErrToolNotFound = errors.New("tool not found")

func (r *Registry) Invoke(ctx context.Context, toolName string, ic InvokeContext, args map[string]any) (map[string]any, error) {
	t, ok := r.Get(toolName)
	if !ok {
		return nil, ErrToolNotFound
	}
	sink := r.sink
	if ic.Sink != nil {
		sink = ic.Sink
	}
	if sink != nil && ic.TaskID != "" {
		sink.Append(Event{
			TaskID: ic.TaskID,
			Type:   EventToolInvoked,
			Time:   time.Now().UTC(),
			Data:   formatToolInvocation(toolName, args),
		})
	}
	return t.Execute(ctx, ic, args)
}

func formatToolInvocation(toolName string, args map[string]any) string {
	if len(args) == 0 {
		return toolName
	}
	b, err := json.Marshal(sanitizeArgs(args))
	if err != nil {
		return toolName
	}
	return fmt.Sprintf("%s %s", toolName, string(b))
}

func sanitizeArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = sanitizeValue(k, v)
	}
	return out
}

func sanitizeValue(key string, v any) any {
	if isSensitiveKey(key) {
		return "[REDACTED]"
	}
	switch x := v.(type) {
	case string:
		return truncateString(x, 200)
	case []string:
		out := make([]any, 0, len(x))
		for _, s := range x {
			out = append(out, truncateString(s, 200))
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, sanitizeValue("", item))
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = sanitizeValue(k, vv)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = sanitizeValue(k, vv)
		}
		return out
	default:
		return v
	}
}

func isSensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return false
	}
	sensitive := []string{
		"password", "passwd", "secret", "token", "apikey", "api_key",
		"authorization", "cookie", "env",
	}
	for _, s := range sensitive {
		if strings.Contains(key, s) {
			return true
		}
	}
	return false
}

func truncateString(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
