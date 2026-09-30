// Package backend holds the concrete HTTP adapters for the mcp read-only tool
// ports (architecture rule 1: concrete clients only in adapter packages). Each
// adapter is a thin raw-passthrough over a backend's HTTP API: the backend's
// native JSON body is returned to the agent unchanged, so Argus adds no
// interpretation on the tool path. All calls are GETs — read-only by design.
package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// httpClient is the shared transport; a modest timeout bounds a hung backend.
func newHTTP() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

// getRaw performs a GET and returns the response body verbatim. Non-2xx is an
// error carrying a truncated body so the agent sees why a tool failed.
func getRaw(ctx context.Context, hc *http.Client, u, tenant string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if tenant != "" {
		req.Header.Set("X-Scope-OrgID", tenant)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("backend %s: HTTP %d: %s", u, resp.StatusCode, truncate(body, 512))
	}
	return json.RawMessage(body), nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// Mimir is the MetricsBackend/AlertsBackend adapter (Prometheus-compatible API).
type Mimir struct {
	base   string // e.g. http://mimir-gateway.lgtm.svc (no /prometheus suffix)
	tenant string
	hc     *http.Client
}

// NewMimir builds a Mimir adapter for the given base URL and optional tenant.
func NewMimir(base, tenant string) *Mimir { return &Mimir{base: base, tenant: tenant, hc: newHTTP()} }

// QueryInstant runs an instant PromQL query.
func (m *Mimir) QueryInstant(ctx context.Context, query string, at time.Time) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("time", at.UTC().Format(time.RFC3339))
	return getRaw(ctx, m.hc, m.base+"/prometheus/api/v1/query?"+q.Encode(), m.tenant)
}

// QueryRange runs a range PromQL query.
func (m *Mimir) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("start", start.UTC().Format(time.RFC3339))
	q.Set("end", end.UTC().Format(time.RFC3339))
	q.Set("step", strconv.FormatFloat(step.Seconds(), 'f', -1, 64))
	return getRaw(ctx, m.hc, m.base+"/prometheus/api/v1/query_range?"+q.Encode(), m.tenant)
}

// ListAlerts returns alerts, filtered by state client-side when state != "".
// The Prometheus alerts API has no state parameter, so filtering here keeps the
// tool's contract honest rather than silently ignoring the argument.
func (m *Mimir) ListAlerts(ctx context.Context, state string) (json.RawMessage, error) {
	raw, err := getRaw(ctx, m.hc, m.base+"/prometheus/api/v1/alerts", m.tenant)
	if err != nil {
		return nil, err
	}
	if state == "" {
		return raw, nil
	}
	return filterAlertsByState(raw, state)
}

// filterAlertsByState narrows a Prometheus alerts response to a single state.
// On any shape mismatch it returns the raw response rather than an empty set —
// an unexpected schema must not silently drop alerts.
func filterAlertsByState(raw json.RawMessage, state string) (json.RawMessage, error) {
	var doc struct {
		Status string `json:"status"`
		Data   struct {
			Alerts []json.RawMessage `json:"alerts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Status != "success" {
		return raw, nil
	}
	kept := make([]json.RawMessage, 0, len(doc.Data.Alerts))
	for _, a := range doc.Data.Alerts {
		var meta struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(a, &meta); err != nil {
			return raw, nil
		}
		if meta.State == state {
			kept = append(kept, a)
		}
	}
	out := map[string]any{"status": "success", "data": map[string]any{"alerts": kept}}
	return json.Marshal(out)
}

// Loki is the LogsBackend adapter.
type Loki struct {
	base   string // e.g. http://loki-gateway.lgtm.svc
	tenant string
	hc     *http.Client
}

// NewLoki builds a Loki adapter.
func NewLoki(base, tenant string) *Loki { return &Loki{base: base, tenant: tenant, hc: newHTTP()} }

// QueryRange runs a LogQL range query.
func (l *Loki) QueryRange(ctx context.Context, query string, start, end time.Time, limit int) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("start", start.UTC().Format(time.RFC3339Nano))
	q.Set("end", end.UTC().Format(time.RFC3339Nano))
	q.Set("limit", strconv.Itoa(limit))
	return getRaw(ctx, l.hc, l.base+"/loki/api/v1/query_range?"+q.Encode(), l.tenant)
}

// Tempo is the TracesBackend adapter.
type Tempo struct {
	base   string // e.g. http://tempo.lgtm.svc
	tenant string
	hc     *http.Client
}

// NewTempo builds a Tempo adapter.
func NewTempo(base, tenant string) *Tempo { return &Tempo{base: base, tenant: tenant, hc: newHTTP()} }

// Search runs a TraceQL/Tempo search.
func (t *Tempo) Search(ctx context.Context, query string, limit int) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	return getRaw(ctx, t.hc, t.base+"/api/search?"+q.Encode(), t.tenant)
}

// catalogWindow is how far back the discovery calls look. They answer "what
// exists now", and a short recent window is served by the ingesters, which
// keeps it working while a store-gateway's bucket index is stale.
const catalogWindow = 15 * time.Minute

// stringList decodes the {"status":"success","data":[...]} shape the
// Prometheus and Loki label APIs share.
func stringList(raw json.RawMessage) ([]string, error) {
	var doc struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decoding label list: %w", err)
	}
	if doc.Status != "success" {
		return nil, fmt.Errorf("backend answered status %q", doc.Status)
	}
	return doc.Data, nil
}

// labels calls one of the Prometheus label endpoints, scoped to the recent
// window and, when given, to a series selector.
func (m *Mimir) labels(ctx context.Context, path, selector string) ([]string, error) {
	now := time.Now()
	q := url.Values{}
	q.Set("start", now.Add(-catalogWindow).UTC().Format(time.RFC3339))
	q.Set("end", now.UTC().Format(time.RFC3339))
	if selector != "" {
		q.Set("match[]", selector)
	}
	raw, err := getRaw(ctx, m.hc, m.base+"/prometheus/api/v1/"+path+"?"+q.Encode(), m.tenant)
	if err != nil {
		return nil, err
	}
	return stringList(raw)
}

// MetricNames lists the metric names with recent samples.
func (m *Mimir) MetricNames(ctx context.Context, selector string) ([]string, error) {
	return m.labels(ctx, "label/__name__/values", selector)
}

// LabelNames lists the label names on recent series.
func (m *Mimir) LabelNames(ctx context.Context, selector string) ([]string, error) {
	return m.labels(ctx, "labels", selector)
}

// LabelValues lists one label's values on recent series.
func (m *Mimir) LabelValues(ctx context.Context, label, selector string) ([]string, error) {
	return m.labels(ctx, "label/"+url.PathEscape(label)+"/values", selector)
}

func (l *Loki) labels(ctx context.Context, path string) ([]string, error) {
	now := time.Now()
	q := url.Values{}
	q.Set("start", now.Add(-time.Hour).UTC().Format(time.RFC3339Nano))
	q.Set("end", now.UTC().Format(time.RFC3339Nano))
	raw, err := getRaw(ctx, l.hc, l.base+"/loki/api/v1/"+path+"?"+q.Encode(), l.tenant)
	if err != nil {
		return nil, err
	}
	return stringList(raw)
}

// LogLabelNames lists the stream labels seen in the last hour.
func (l *Loki) LogLabelNames(ctx context.Context) ([]string, error) {
	return l.labels(ctx, "labels")
}

// LogLabelValues lists one stream label's values over the last hour.
func (l *Loki) LogLabelValues(ctx context.Context, label string) ([]string, error) {
	return l.labels(ctx, "label/"+url.PathEscape(label)+"/values")
}

// TraceTagNames lists searchable attribute names, scoped as TraceQL writes
// them: resource.service.name, span.http.method. Intrinsics (duration, status,
// name, …) have no scope prefix.
func (t *Tempo) TraceTagNames(ctx context.Context) ([]string, error) {
	raw, err := getRaw(ctx, t.hc, t.base+"/api/v2/search/tags", t.tenant)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Scopes []struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decoding trace tags: %w", err)
	}
	var names []string
	for _, s := range doc.Scopes {
		for _, tag := range s.Tags {
			if s.Name == "intrinsic" || s.Name == "" {
				names = append(names, tag)
			} else {
				names = append(names, s.Name+"."+tag)
			}
		}
	}
	return names, nil
}

// TraceTagValues lists the values of one scoped attribute.
func (t *Tempo) TraceTagValues(ctx context.Context, tag string) ([]string, error) {
	raw, err := getRaw(ctx, t.hc, t.base+"/api/v2/search/tag/"+url.PathEscape(tag)+"/values", t.tenant)
	if err != nil {
		return nil, err
	}
	var doc struct {
		TagValues []struct {
			Value string `json:"value"`
		} `json:"tagValues"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decoding trace tag values: %w", err)
	}
	values := make([]string, 0, len(doc.TagValues))
	for _, v := range doc.TagValues {
		values = append(values, v.Value)
	}
	return values, nil
}
