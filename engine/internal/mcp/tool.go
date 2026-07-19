package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Tool is one MCP tool exposed to agents. Handlers are read-only by
// construction — the backend ports (ports.go) have no mutating methods — so
// ReadOnly is always true here and is surfaced to clients as the MCP
// readOnlyHint annotation.
type Tool struct {
	Name        string                                                                   `json:"name"`
	Description string                                                                   `json:"description"`
	InputSchema json.RawMessage                                                          `json:"inputSchema"`
	ReadOnly    bool                                                                     `json:"-"`
	Handler     func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) `json:"-"`
}

// Registry holds the tool set in registration order. It is the transport-
// agnostic core: the JSON-RPC/stdio MCP transport (a later slice) drives
// List and Call, and so do unit tests.
type Registry struct {
	tools map[string]Tool
	order []string
}

// NewRegistry returns an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds a tool. Empty names, nil handlers, and duplicates are errors: a
// silently dropped or shadowed tool changes the surface every agent sees, which
// would make a benchmark comparison unfair.
func (r *Registry) Register(t Tool) error {
	if t.Name == "" {
		return fmt.Errorf("mcp: tool with empty name")
	}
	if t.Handler == nil {
		return fmt.Errorf("mcp: tool %q has nil handler", t.Name)
	}
	if _, dup := r.tools[t.Name]; dup {
		return fmt.Errorf("mcp: duplicate tool %q", t.Name)
	}
	r.tools[t.Name] = t
	r.order = append(r.order, t.Name)
	return nil
}

// List returns the registered tools in registration order (stable across
// processes so tools/list is deterministic).
func (r *Registry) List() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

// Call dispatches a tool invocation. An unknown tool is an error, never a
// silent empty result.
func (r *Registry) Call(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("mcp: unknown tool %q", name)
	}
	return t.Handler(ctx, args)
}

// strictUnmarshal decodes tool arguments, rejecting unknown fields so a
// misnamed argument fails loudly instead of being silently ignored.
func strictUnmarshal(args json.RawMessage, v any) error {
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("mcp: bad arguments: %w", err)
	}
	return nil
}
