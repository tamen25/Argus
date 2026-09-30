package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Discovery tool names.
const (
	ToolListMetrics      = "list_metrics"
	ToolListMetricLabels = "list_metric_labels"
	ToolListLogLabels    = "list_log_labels"
	ToolListTraceTags    = "list_trace_tags"
)

// Limits on a discovery answer. A busy Mimir holds thousands of metric names;
// handed over whole, they would fill a model's context before it asked a real
// question. The tool caps the list and says how many there were, and the agent
// narrows it with match or a selector.
const (
	defaultListLimit = 100
	maxListLimit     = 500
)

// listing is the answer every discovery tool gives.
type listing struct {
	// Total is how many values matched before the cap.
	Total    int `json:"total"`
	Returned int `json:"returned"`
	// Truncated is set when the cap cut the list; narrow the request to see the rest.
	Truncated bool     `json:"truncated"`
	Values    []string `json:"values"`
}

// listResult filters values by a case-insensitive substring, sorts them, and
// applies the cap.
func listResult(values []string, match string, limit int) (json.RawMessage, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	match = strings.ToLower(strings.TrimSpace(match))
	kept := make([]string, 0, len(values))
	for _, v := range values {
		if match == "" || strings.Contains(strings.ToLower(v), match) {
			kept = append(kept, v)
		}
	}
	sort.Strings(kept)
	out := listing{Total: len(kept), Values: kept}
	if len(kept) > limit {
		out.Values = kept[:limit]
		out.Truncated = true
	}
	out.Returned = len(out.Values)
	return json.Marshal(out)
}

const (
	matchProp = `"match":{"type":"string","description":"Keep only values containing this text (case-insensitive)"}`
	limitProp = `"limit":{"type":"integer","description":"Max values to return; default 100, at most 500"}`
)

func metricsListTool(c MetricsCatalog) Tool {
	type args struct {
		Match    string `json:"match,omitempty"`
		Selector string `json:"selector,omitempty"`
		Limit    int    `json:"limit,omitempty"`
	}
	schema := `{"type":"object","additionalProperties":false,"properties":{` +
		matchProp + `,` +
		`"selector":{"type":"string","description":"PromQL series selector to scope the list, e.g. {job=\"my-service\"}"},` +
		limitProp + `}}`
	return Tool{
		Name: ToolListMetrics,
		Description: "List the metric names that currently exist in Mimir (read-only). " +
			"Use it before " + ToolQueryPrometheus + ": metric names differ between environments.",
		InputSchema: json.RawMessage(schema),
		ReadOnly:    true,
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var a args
			if err := strictUnmarshal(raw, &a); err != nil {
				return nil, err
			}
			names, err := c.MetricNames(ctx, a.Selector)
			if err != nil {
				return nil, err
			}
			return listResult(names, a.Match, a.Limit)
		},
	}
}

func metricLabelsTool(c MetricsCatalog) Tool {
	type args struct {
		Label    string `json:"label,omitempty"`
		Selector string `json:"selector,omitempty"`
		Match    string `json:"match,omitempty"`
		Limit    int    `json:"limit,omitempty"`
	}
	schema := `{"type":"object","additionalProperties":false,"properties":{` +
		`"label":{"type":"string","description":"A label name: list its values. Omit to list the label names themselves."},` +
		`"selector":{"type":"string","description":"PromQL series selector to scope the list, e.g. {__name__=\"my_metric\"}"},` +
		matchProp + `,` + limitProp + `}}`
	return Tool{
		Name: ToolListMetricLabels,
		Description: "List the label names on metrics in Mimir, or the values of one label (read-only). " +
			"Use it to learn which label identifies a service and what values it takes.",
		InputSchema: json.RawMessage(schema),
		ReadOnly:    true,
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var a args
			if err := strictUnmarshal(raw, &a); err != nil {
				return nil, err
			}
			var (
				values []string
				err    error
			)
			if a.Label == "" {
				values, err = c.LabelNames(ctx, a.Selector)
			} else {
				values, err = c.LabelValues(ctx, a.Label, a.Selector)
			}
			if err != nil {
				return nil, err
			}
			return listResult(values, a.Match, a.Limit)
		},
	}
}

func logLabelsTool(c LogsCatalog) Tool {
	type args struct {
		Label string `json:"label,omitempty"`
		Match string `json:"match,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	schema := `{"type":"object","additionalProperties":false,"properties":{` +
		`"label":{"type":"string","description":"A label name: list its values. Omit to list the label names themselves."},` +
		matchProp + `,` + limitProp + `}}`
	return Tool{
		Name: ToolListLogLabels,
		Description: "List the stream labels logs are indexed by in Loki, or the values of one label (read-only). " +
			"Use it before " + ToolQueryLoki + " to build a valid stream selector.",
		InputSchema: json.RawMessage(schema),
		ReadOnly:    true,
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var a args
			if err := strictUnmarshal(raw, &a); err != nil {
				return nil, err
			}
			var (
				values []string
				err    error
			)
			if a.Label == "" {
				values, err = c.LogLabelNames(ctx)
			} else {
				values, err = c.LogLabelValues(ctx, a.Label)
			}
			if err != nil {
				return nil, err
			}
			return listResult(values, a.Match, a.Limit)
		},
	}
}

func traceTagsTool(c TracesCatalog) Tool {
	type args struct {
		Tag   string `json:"tag,omitempty"`
		Match string `json:"match,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	schema := `{"type":"object","additionalProperties":false,"properties":{` +
		`"tag":{"type":"string","description":"A scoped attribute name, e.g. resource.service.name: list its values. Omit to list the attribute names."},` +
		matchProp + `,` + limitProp + `}}`
	return Tool{
		Name: ToolListTraceTags,
		Description: "List the attributes traces can be searched by in Tempo, or the values of one attribute (read-only). " +
			"Names are scoped as TraceQL writes them (resource.…, span.…). Use it before " + ToolSearchTraces + ".",
		InputSchema: json.RawMessage(schema),
		ReadOnly:    true,
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var a args
			if err := strictUnmarshal(raw, &a); err != nil {
				return nil, err
			}
			var (
				values []string
				err    error
			)
			if a.Tag == "" {
				values, err = c.TraceTagNames(ctx)
			} else {
				values, err = c.TraceTagValues(ctx, a.Tag)
			}
			if err != nil {
				return nil, fmt.Errorf("%s: %w", ToolListTraceTags, err)
			}
			return listResult(values, a.Match, a.Limit)
		},
	}
}
