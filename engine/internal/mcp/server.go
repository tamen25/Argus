package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Tool names. The same set is offered to every agent so a benchmark compares
// agents, not tool access (master plan §3.2).
const (
	ToolQueryPrometheus = "query_prometheus"
	ToolQueryLoki       = "query_loki"
	ToolSearchTraces    = "search_traces"
	ToolGetK8sTopology  = "get_k8s_topology"
	ToolListAlerts      = "list_alerts"
)

// NewServer builds a Registry with the read-only tool surface. Only tools whose
// backend is present in b are registered — a partial deployment exposes a
// smaller, honest surface rather than tools that fail at call time. An empty
// surface (no backends) is an error: an agent with no tools cannot be scored.
func NewServer(b Backends) (*Registry, error) {
	r := NewRegistry()

	if b.Metrics != nil {
		if err := r.Register(promTool(b.Metrics)); err != nil {
			return nil, err
		}
	}
	if b.Logs != nil {
		if err := r.Register(lokiTool(b.Logs)); err != nil {
			return nil, err
		}
	}
	if b.Traces != nil {
		if err := r.Register(tracesTool(b.Traces)); err != nil {
			return nil, err
		}
	}
	if b.Topology != nil {
		if err := r.Register(topologyTool(b.Topology)); err != nil {
			return nil, err
		}
	}
	if b.Alerts != nil {
		if err := r.Register(alertsTool(b.Alerts)); err != nil {
			return nil, err
		}
	}

	if len(r.List()) == 0 {
		return nil, fmt.Errorf("mcp: no backends configured; tool surface is empty")
	}
	return r, nil
}

func promTool(m MetricsBackend) Tool {
	type args struct {
		Query string `json:"query"`
		Time  string `json:"time,omitempty"`
		Start string `json:"start,omitempty"`
		End   string `json:"end,omitempty"`
		Step  string `json:"step,omitempty"`
	}
	schema := `{"type":"object","required":["query"],"additionalProperties":false,` +
		`"properties":{` +
		`"query":{"type":"string","description":"PromQL expression"},` +
		`"time":{"type":"string","description":"RFC3339 instant; default now. Ignored if start/end set."},` +
		`"start":{"type":"string","description":"RFC3339 range start (with end+step)"},` +
		`"end":{"type":"string","description":"RFC3339 range end"},` +
		`"step":{"type":"string","description":"Range step duration, e.g. 30s"}}}`
	return Tool{
		Name:        ToolQueryPrometheus,
		Description: "Run a read-only PromQL query against Mimir. Instant by default; range if start, end and step are given.",
		InputSchema: json.RawMessage(schema),
		ReadOnly:    true,
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var a args
			if err := strictUnmarshal(raw, &a); err != nil {
				return nil, err
			}
			if a.Query == "" {
				return nil, fmt.Errorf("mcp: %s: query is required", ToolQueryPrometheus)
			}
			if a.Start != "" || a.End != "" || a.Step != "" {
				start, err := parseTime(a.Start)
				if err != nil {
					return nil, fmt.Errorf("start: %w", err)
				}
				end, err := parseTime(a.End)
				if err != nil {
					return nil, fmt.Errorf("end: %w", err)
				}
				step, err := time.ParseDuration(a.Step)
				if err != nil {
					return nil, fmt.Errorf("step: %w", err)
				}
				return m.QueryRange(ctx, a.Query, start, end, step)
			}
			at := time.Now()
			if a.Time != "" {
				t, err := parseTime(a.Time)
				if err != nil {
					return nil, fmt.Errorf("time: %w", err)
				}
				at = t
			}
			return m.QueryInstant(ctx, a.Query, at)
		},
	}
}

func lokiTool(l LogsBackend) Tool {
	type args struct {
		Query string `json:"query"`
		Start string `json:"start,omitempty"`
		End   string `json:"end,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	schema := `{"type":"object","required":["query"],"additionalProperties":false,` +
		`"properties":{` +
		`"query":{"type":"string","description":"LogQL query"},` +
		`"start":{"type":"string","description":"RFC3339 start; default 1h ago"},` +
		`"end":{"type":"string","description":"RFC3339 end; default now"},` +
		`"limit":{"type":"integer","description":"Max log lines; default 100"}}}`
	return Tool{
		Name:        ToolQueryLoki,
		Description: "Run a read-only LogQL range query against Loki.",
		InputSchema: json.RawMessage(schema),
		ReadOnly:    true,
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var a args
			if err := strictUnmarshal(raw, &a); err != nil {
				return nil, err
			}
			if a.Query == "" {
				return nil, fmt.Errorf("mcp: %s: query is required", ToolQueryLoki)
			}
			end := time.Now()
			if a.End != "" {
				t, err := parseTime(a.End)
				if err != nil {
					return nil, fmt.Errorf("end: %w", err)
				}
				end = t
			}
			start := end.Add(-time.Hour)
			if a.Start != "" {
				t, err := parseTime(a.Start)
				if err != nil {
					return nil, fmt.Errorf("start: %w", err)
				}
				start = t
			}
			limit := a.Limit
			if limit <= 0 {
				limit = 100
			}
			return l.QueryRange(ctx, a.Query, start, end, limit)
		},
	}
}

func tracesTool(tb TracesBackend) Tool {
	type args struct {
		Query string `json:"query"`
		Limit int    `json:"limit,omitempty"`
	}
	schema := `{"type":"object","required":["query"],"additionalProperties":false,` +
		`"properties":{` +
		`"query":{"type":"string","description":"TraceQL / Tempo search query"},` +
		`"limit":{"type":"integer","description":"Max traces; default 20"}}}`
	return Tool{
		Name:        ToolSearchTraces,
		Description: "Search traces in Tempo (read-only).",
		InputSchema: json.RawMessage(schema),
		ReadOnly:    true,
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var a args
			if err := strictUnmarshal(raw, &a); err != nil {
				return nil, err
			}
			if a.Query == "" {
				return nil, fmt.Errorf("mcp: %s: query is required", ToolSearchTraces)
			}
			limit := a.Limit
			if limit <= 0 {
				limit = 20
			}
			return tb.Search(ctx, a.Query, limit)
		},
	}
}

func topologyTool(tp TopologyBackend) Tool {
	type args struct {
		Namespace string `json:"namespace,omitempty"`
	}
	schema := `{"type":"object","additionalProperties":false,` +
		`"properties":{"namespace":{"type":"string","description":"Namespace to scope topology; empty = all"}}}`
	return Tool{
		Name:        ToolGetK8sTopology,
		Description: "Return service/Kubernetes topology (read-only).",
		InputSchema: json.RawMessage(schema),
		ReadOnly:    true,
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var a args
			if err := strictUnmarshal(raw, &a); err != nil {
				return nil, err
			}
			return tp.Topology(ctx, a.Namespace)
		},
	}
}

func alertsTool(ab AlertsBackend) Tool {
	type args struct {
		State string `json:"state,omitempty"`
	}
	schema := `{"type":"object","additionalProperties":false,` +
		`"properties":{"state":{"type":"string","description":"Filter by alert state (e.g. firing, pending); empty = all"}}}`
	return Tool{
		Name:        ToolListAlerts,
		Description: "List alerts and their state (read-only).",
		InputSchema: json.RawMessage(schema),
		ReadOnly:    true,
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var a args
			if err := strictUnmarshal(raw, &a); err != nil {
				return nil, err
			}
			return ab.ListAlerts(ctx, a.State)
		},
	}
}

// parseTime accepts RFC3339; an empty string is an error (callers decide
// defaults before calling).
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	return time.Parse(time.RFC3339, s)
}
