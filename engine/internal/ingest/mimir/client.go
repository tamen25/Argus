// Package mimir is the concrete adapter for the MimirAPI port (architecture
// rule 1: concrete clients only in adapter packages). Read-only by design.
package mimir

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/tamen25/Argus/engine/internal/bounded"
)

// Client talks to Mimir's Prometheus-compatible HTTP API.
type Client struct {
	base   string // e.g. http://mimir-gateway.lgtm.svc (without /prometheus)
	tenant string // X-Scope-OrgID; empty for anonymous single-tenant
	http   *http.Client
}

// New builds a client for the given base URL and optional tenant.
func New(base, tenant string) *Client {
	return &Client{base: base, tenant: tenant, http: &http.Client{Timeout: 30 * time.Second}}
}

// LabelValues implements ingest.MimirAPI.
func (c *Client) LabelValues(ctx context.Context, label string) ([]string, error) {
	var out struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	u := fmt.Sprintf("%s/prometheus/api/v1/label/%s/values", c.base, url.PathEscape(label))
	if err := c.get(ctx, u, &out); err != nil {
		return nil, err
	}
	if out.Status != "success" {
		return nil, fmt.Errorf("mimir label values: status %q", out.Status)
	}
	return out.Data, nil
}

// labelValuesResponse is the response of Mimir's label-values cardinality
// endpoint (GET <prefix>/api/v1/cardinality/label_values), as documented in
// the Mimir HTTP API reference. The counts are pointers so a field Mimir did
// not send is told apart from a measured zero.
type labelValuesResponse struct {
	SeriesCountTotal *int64 `json:"series_count_total"`
	Labels           []struct {
		LabelName        string `json:"label_name"`
		LabelValuesCount *int64 `json:"label_values_count"`
		SeriesCount      *int64 `json:"series_count"`
		// Cardinality is the top values by series count, at most the limit
		// asked for.
		Cardinality []struct {
			LabelValue  string `json:"label_value"`
			SeriesCount int64  `json:"series_count"`
		} `json:"cardinality"`
	} `json:"labels"`
}

// maxCardinalityLimit is the largest limit the endpoint accepts.
const maxCardinalityLimit = 500

// labelValues calls the label-values cardinality endpoint for one label,
// counting active series (what ingesters hold now, the cost driver) rather
// than the in-memory default, which includes series no longer written.
func (c *Client) labelValues(ctx context.Context, selector, label string, limit int) (labelValuesResponse, error) {
	q := url.Values{}
	if selector != "" {
		q.Set("selector", selector)
	}
	q.Add("label_names[]", label)
	q.Set("count_method", "active")
	q.Set("limit", strconv.Itoa(limit))
	var out labelValuesResponse
	u := fmt.Sprintf("%s/prometheus/api/v1/cardinality/label_values?%s", c.base, q.Encode())
	if err := c.get(ctx, u, &out); err != nil {
		return labelValuesResponse{}, err
	}
	return out, nil
}

// LabelCardinality implements ingest.MimirAPI: the number of distinct values
// of label among the series selector matches. When no series matched, Mimir
// does not list the label, and the answer is 0.
func (c *Client) LabelCardinality(ctx context.Context, selector, label string) (int64, error) {
	out, err := c.labelValues(ctx, selector, label, 0) // the count only, no per-value list
	if err != nil {
		return 0, err
	}
	for _, l := range out.Labels {
		if l.LabelName != label {
			continue
		}
		if l.LabelValuesCount == nil {
			return 0, fmt.Errorf("mimir cardinality for %s: response has no label_values_count", label)
		}
		return *l.LabelValuesCount, nil
	}
	return 0, nil
}

// SeriesCountByLabel returns the active series count per value of a label,
// the basis for active-series cost attribution (label "job" → series per
// service).
//
// Mimir lists at most 500 values. The counts always add up to the tenant's
// total anyway: series of values past the list go to an "(other: N more
// values)" entry, and series without the label to "(no <label> label)". A
// fleet larger than the list is then visible in the report rather than
// silently missing from it.
func (c *Client) SeriesCountByLabel(ctx context.Context, label string) (map[string]int64, error) {
	out, err := c.labelValues(ctx, "", label, maxCardinalityLimit)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	var attributed int64
	for _, l := range out.Labels {
		if l.LabelName != label {
			continue
		}
		for _, v := range l.Cardinality {
			counts[v.LabelValue] = v.SeriesCount
			attributed += v.SeriesCount
		}
		if l.LabelValuesCount == nil || l.SeriesCount == nil {
			return nil, fmt.Errorf("mimir cardinality for %s: response has no label_values_count or series_count", label)
		}
		if more := *l.LabelValuesCount - int64(len(l.Cardinality)); more > 0 {
			counts[fmt.Sprintf("(other: %d more values)", more)] = *l.SeriesCount - attributed
		}
		attributed = *l.SeriesCount
	}
	if out.SeriesCountTotal != nil {
		if unlabeled := *out.SeriesCountTotal - attributed; unlabeled > 0 {
			counts[fmt.Sprintf("(no %s label)", label)] = unlabeled
		}
	}
	return counts, nil
}

// SeriesSource adapts a Client to cost.SeriesSource for a chosen service
// label (default "service_name"). Kept here so cost/ never imports a concrete
// client; the method set satisfies the port structurally.
type SeriesSource struct {
	Client *Client
	Label  string
}

// ActiveSeriesByService satisfies cost.SeriesSource.
func (s SeriesSource) ActiveSeriesByService(ctx context.Context) (map[string]int64, error) {
	label := s.Label
	if label == "" {
		label = "service_name"
	}
	return s.Client.SeriesCountByLabel(ctx, label)
}

func (c *Client) get(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if c.tenant != "" {
		req.Header.Set("X-Scope-OrgID", c.tenant)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("mimir %s: HTTP %d: %s", u, resp.StatusCode, b)
	}
	return bounded.DecodeJSON(resp.Body, bounded.BackendResponse, into)
}
