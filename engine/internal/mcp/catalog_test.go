package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// fakeCatalog answers every discovery port from fixed lists and records what
// it was asked.
type fakeCatalog struct {
	names    []string
	lastCall string
}

func (c *fakeCatalog) MetricNames(_ context.Context, selector string) ([]string, error) {
	c.lastCall = "metric-names " + selector
	return c.names, nil
}
func (c *fakeCatalog) LabelNames(_ context.Context, selector string) ([]string, error) {
	c.lastCall = "label-names " + selector
	return c.names, nil
}
func (c *fakeCatalog) LabelValues(_ context.Context, label, selector string) ([]string, error) {
	c.lastCall = "label-values " + label + " " + selector
	return c.names, nil
}
func (c *fakeCatalog) LogLabelNames(_ context.Context) ([]string, error) {
	c.lastCall = "log-label-names"
	return c.names, nil
}
func (c *fakeCatalog) LogLabelValues(_ context.Context, label string) ([]string, error) {
	c.lastCall = "log-label-values " + label
	return c.names, nil
}
func (c *fakeCatalog) TraceTagNames(_ context.Context) ([]string, error) {
	c.lastCall = "trace-tag-names"
	return c.names, nil
}
func (c *fakeCatalog) TraceTagValues(_ context.Context, tag string) ([]string, error) {
	c.lastCall = "trace-tag-values " + tag
	return c.names, nil
}

func decodeListing(t *testing.T, raw json.RawMessage) listing {
	t.Helper()
	var l listing
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatalf("not a listing: %s", raw)
	}
	return l
}

func TestListResult_FiltersSortsAndCaps(t *testing.T) {
	raw, err := listResult([]string{"zeta_total", "Alpha_seconds", "beta_total", "gamma"}, "TOTAL", 0)
	if err != nil {
		t.Fatal(err)
	}
	l := decodeListing(t, raw)
	if strings.Join(l.Values, ",") != "beta_total,zeta_total" || l.Total != 2 || l.Returned != 2 || l.Truncated {
		t.Errorf("filtered listing = %+v, want the two *_total names, sorted, untruncated", l)
	}

	// A list longer than the limit is cut, and says so with the real total, so
	// the agent knows to narrow rather than assume it saw everything.
	many := make([]string, 700)
	for i := range many {
		many[i] = fmt.Sprintf("metric_%03d", i)
	}
	raw, _ = listResult(many, "", 3)
	l = decodeListing(t, raw)
	if l.Total != 700 || l.Returned != 3 || !l.Truncated || l.Values[0] != "metric_000" {
		t.Errorf("capped listing = total %d returned %d truncated %v first %q", l.Total, l.Returned, l.Truncated, l.Values[0])
	}
	// The default and the maximum both hold.
	if l = decodeListing(t, must(listResult(many, "", 0))); l.Returned != defaultListLimit {
		t.Errorf("default limit returned %d, want %d", l.Returned, defaultListLimit)
	}
	if l = decodeListing(t, must(listResult(many, "", 100000))); l.Returned != maxListLimit {
		t.Errorf("an oversized limit returned %d, want the maximum %d", l.Returned, maxListLimit)
	}
}

func must(raw json.RawMessage, err error) json.RawMessage {
	if err != nil {
		panic(err)
	}
	return raw
}

func TestCatalogTools_RouteToTheRightPort(t *testing.T) {
	c := &fakeCatalog{names: []string{"a", "b"}}
	reg, err := NewServer(Backends{MetricsCatalog: c, LogsCatalog: c, TracesCatalog: c})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		tool, args, want string
	}{
		{ToolListMetrics, `{"selector":"{job=\"x\"}"}`, `metric-names {job="x"}`},
		{ToolListMetricLabels, `{}`, "label-names "},
		{ToolListMetricLabels, `{"label":"job","selector":"up"}`, "label-values job up"},
		{ToolListLogLabels, `{}`, "log-label-names"},
		{ToolListLogLabels, `{"label":"service_name"}`, "log-label-values service_name"},
		{ToolListTraceTags, `{}`, "trace-tag-names"},
		{ToolListTraceTags, `{"tag":"resource.service.name"}`, "trace-tag-values resource.service.name"},
	}
	for _, tc := range cases {
		raw, err := reg.Call(context.Background(), tc.tool, json.RawMessage(tc.args))
		if err != nil {
			t.Fatalf("%s %s: %v", tc.tool, tc.args, err)
		}
		if c.lastCall != tc.want {
			t.Errorf("%s %s reached %q, want %q", tc.tool, tc.args, c.lastCall, tc.want)
		}
		if l := decodeListing(t, raw); l.Total != 2 {
			t.Errorf("%s: listing = %+v", tc.tool, l)
		}
	}
	// Strict arguments, like every other tool.
	if _, err := reg.Call(context.Background(), ToolListMetrics, json.RawMessage(`{"metric":"x"}`)); err == nil {
		t.Error("an unknown argument was accepted")
	}
}

// Discovery and topology are listed first, and the query tools point at the
// discovery tools only when those are actually offered.
func TestSurface_DiscoveryFirstAndHintsOnlyWhenOffered(t *testing.T) {
	f := &fakeBackend{}
	c := &fakeCatalog{}
	b := fullBackends(f)
	b.MetricsCatalog, b.LogsCatalog, b.TracesCatalog = c, c, c
	reg, err := NewServer(b)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	desc := map[string]string{}
	for _, tool := range reg.List() {
		names = append(names, tool.Name)
		desc[tool.Name] = tool.Description
	}
	want := strings.Join([]string{
		ToolGetK8sTopology, ToolListMetrics, ToolListMetricLabels, ToolListLogLabels, ToolListTraceTags,
		ToolQueryPrometheus, ToolQueryLoki, ToolSearchTraces, ToolListAlerts,
	}, ",")
	if strings.Join(names, ",") != want {
		t.Errorf("tool order = %v, want %s", names, want)
	}
	if !strings.Contains(desc[ToolQueryPrometheus], ToolListMetrics) {
		t.Errorf("query_prometheus does not point at list_metrics: %q", desc[ToolQueryPrometheus])
	}

	// Without the catalogs, no description may send an agent to a missing tool.
	bare, err := NewServer(fullBackends(f))
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range bare.List() {
		for _, missing := range []string{ToolListMetrics, ToolListLogLabels, ToolListTraceTags} {
			if strings.Contains(tool.Description, missing) {
				t.Errorf("%s points at %s, which is not offered", tool.Name, missing)
			}
		}
	}
}

// No description may carry a name from the environment under test: every
// agent reads them, on every scenario.
func TestDescriptions_UseOnlyNeutralExamples(t *testing.T) {
	f := &fakeBackend{}
	c := &fakeCatalog{}
	b := fullBackends(f)
	b.MetricsCatalog, b.LogsCatalog, b.TracesCatalog = c, c, c
	reg, err := NewServer(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range reg.List() {
		text := tool.Description + string(tool.InputSchema)
		for _, env := range []string{"otel-demo", "frontend", "checkout", "cart", "payment", "valkey"} {
			if strings.Contains(text, env) {
				t.Errorf("%s mentions %q from the test environment", tool.Name, env)
			}
		}
	}
}
