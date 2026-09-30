package mcp

import (
	"context"
	"encoding/json"
	"time"
)

// The mcp package exposes a read-only tool surface to bench agents. "Read-only"
// is enforced structurally, not by convention: every backend port below is a
// query with no mutating method, so no tool can change user infrastructure
// (architecture rule 5). Concrete clients (Mimir, Loki, Tempo, K8s) live in
// adapter packages and are injected via Backends; unit tests use fakes.
//
// Handlers return json.RawMessage so the backend's native response shape passes
// through to the agent unchanged — Argus adds no interpretation on this path.

// MetricsBackend answers PromQL queries (Mimir/Prometheus HTTP API).
type MetricsBackend interface {
	QueryInstant(ctx context.Context, query string, at time.Time) (json.RawMessage, error)
	QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (json.RawMessage, error)
}

// LogsBackend answers LogQL range queries (Loki).
type LogsBackend interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int) (json.RawMessage, error)
}

// TracesBackend searches traces (Tempo).
type TracesBackend interface {
	Search(ctx context.Context, query string, limit int) (json.RawMessage, error)
}

// TopologyBackend returns service/Kubernetes topology for a namespace.
type TopologyBackend interface {
	Topology(ctx context.Context, namespace string) (json.RawMessage, error)
}

// AlertsBackend lists alerts in a given state (empty = all).
type AlertsBackend interface {
	ListAlerts(ctx context.Context, state string) (json.RawMessage, error)
}

// The catalog ports answer "what telemetry exists here?". Metric names, label
// names and attribute names differ between environments, and a query on a name
// that does not exist returns an empty result rather than an error — so an agent
// without these can spend its whole budget on guesses. They are what a human
// gets from a metric browser.
//
// Unlike the query ports they return plain lists, not the backend's raw body:
// a list has to be filtered and capped before it goes to a model, and that
// needs the values, not an opaque document.

// MetricsCatalog lists metric names and the labels on them. selector is an
// optional PromQL series selector (e.g. `{job="cart"}`) that narrows the scope.
type MetricsCatalog interface {
	MetricNames(ctx context.Context, selector string) ([]string, error)
	LabelNames(ctx context.Context, selector string) ([]string, error)
	LabelValues(ctx context.Context, label, selector string) ([]string, error)
}

// LogsCatalog lists the stream labels logs are indexed by, and their values.
type LogsCatalog interface {
	LogLabelNames(ctx context.Context) ([]string, error)
	LogLabelValues(ctx context.Context, label string) ([]string, error)
}

// TracesCatalog lists the attributes traces can be searched by, and their
// values. Names are scoped the way TraceQL writes them (resource.service.name).
type TracesCatalog interface {
	TraceTagNames(ctx context.Context) ([]string, error)
	TraceTagValues(ctx context.Context, tag string) ([]string, error)
}

// Backends bundles the ports a Server needs. A nil backend disables its tool:
// NewServer only registers a tool whose backend is present, so a partial
// deployment exposes a smaller, honest surface rather than tools that error.
type Backends struct {
	Metrics  MetricsBackend
	Logs     LogsBackend
	Traces   TracesBackend
	Topology TopologyBackend
	Alerts   AlertsBackend

	MetricsCatalog MetricsCatalog
	LogsCatalog    LogsCatalog
	TracesCatalog  TracesCatalog
}
