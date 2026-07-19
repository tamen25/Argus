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

// Backends bundles the ports a Server needs. A nil backend disables its tool:
// NewServer only registers a tool whose backend is present, so a partial
// deployment exposes a smaller, honest surface rather than tools that error.
type Backends struct {
	Metrics  MetricsBackend
	Logs     LogsBackend
	Traces   TracesBackend
	Topology TopologyBackend
	Alerts   AlertsBackend
}
