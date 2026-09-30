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

	// Discovery and topology come first in the list: they are what an agent
	// should reach for before it queries, and models read tool lists in order.
	var tools []Tool
	if b.Topology != nil {
		tools = append(tools, topologyTool(b.Topology))
	}
	if b.MetricsCatalog != nil {
		tools = append(tools, metricsListTool(b.MetricsCatalog), metricLabelsTool(b.MetricsCatalog))
	}
	if b.LogsCatalog != nil {
		tools = append(tools, logLabelsTool(b.LogsCatalog))
	}
	if b.TracesCatalog != nil {
		tools = append(tools, traceTagsTool(b.TracesCatalog))
	}
	if b.Metrics != nil {
		tools = append(tools, promTool(b.Metrics, b.MetricsCatalog != nil))
	}
	if b.Logs != nil {
		tools = append(tools, lokiTool(b.Logs, b.LogsCatalog != nil))
	}
	if b.Traces != nil {
		tools = append(tools, tracesTool(b.Traces, b.TracesCatalog != nil))
	}
	if b.Alerts != nil {
		tools = append(tools, alertsTool(b.Alerts))
	}
	for _, t := range tools {
		if err := r.Register(t); err != nil {
			return nil, err
		}
	}

	if len(r.List()) == 0 {
		return nil, fmt.Errorf("mcp: no backends configured; tool surface is empty")
	}
	return r, nil
}

// hint appends a pointer to the discovery tool, but only when that tool is part
// of the surface: a description must not send an agent to a tool it was not
// given.
func hint(description string, available bool, pointer string) string {
	if !available {
		return description
	}
	return description + " " + pointer
}

func promTool(m MetricsBackend, catalog bool) Tool {
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
		Name: ToolQueryPrometheus,
		Description: hint("Run a read-only PromQL query against Mimir. Instant by default; range if start, end and step are given. "+
			"A query on a metric or label that does not exist returns an empty result, not an error.",
			catalog, "Find real names with "+ToolListMetrics+" and "+ToolListMetricLabels+" first."),
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

func lokiTool(l LogsBackend, catalog bool) Tool {
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
		Name: ToolQueryLoki,
		Description: hint("Run a read-only LogQL range query against Loki, e.g. {label=\"value\"} |= \"text\". "+
			"The limit is this tool's own argument, not part of the query.",
			catalog, "Find the stream labels and their values with "+ToolListLogLabels+" first."),
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

func tracesTool(tb TracesBackend, catalog bool) Tool {
	type args struct {
		Query string `json:"query"`
		Limit int    `json:"limit,omitempty"`
	}
	schema := `{"type":"object","required":["query"],"additionalProperties":false,` +
		`"properties":{` +
		`"query":{"type":"string","description":"TraceQL / Tempo search query"},` +
		`"limit":{"type":"integer","description":"Max traces; default 20"}}}`
	return Tool{
		Name: ToolSearchTraces,
		Description: hint("Search traces in Tempo with a TraceQL query (read-only), e.g. "+
			"{ resource.service.name = \"my-service\" && status = error }. "+
			"The limit is this tool's own argument, not part of the query.",
			catalog, "Find attribute names and values with "+ToolListTraceTags+" first."),
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
		Name: ToolGetK8sTopology,
		Description: "List the cluster's workloads (kind, namespace, name) and, where trace-derived service-graph " +
			"metrics exist, which service calls which (read-only). Workloads are listed by identity only, without " +
			"status. Use it to learn the exact kind, namespace and name of what you are diagnosing.",
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
