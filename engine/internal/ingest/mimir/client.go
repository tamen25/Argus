// Package mimir is the concrete adapter for the MimirAPI port (architecture
// rule 1: concrete clients only in adapter packages). Read-only by design.
package mimir

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
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

// LabelCardinality implements ingest.MimirAPI via Mimir's cardinality API,
// returning the distinct-value count of a label on a metric.
func (c *Client) LabelCardinality(ctx context.Context, metric, label string) (int64, error) {
	var out struct {
		Labels []struct {
			LabelName           string `json:"label_name"`
			SeriesCount         int64  `json:"series_count"`
			DistinctLabelValues int64  `json:"distinct_label_values_count"`
		} `json:"labels"`
	}
	q := url.Values{}
	q.Set("selector", fmt.Sprintf(`{__name__=%q}`, metric))
	q.Add("label_names[]", label)
	u := fmt.Sprintf("%s/prometheus/api/v1/cardinality/label_values?%s", c.base, q.Encode())
	if err := c.get(ctx, u, &out); err != nil {
		return 0, err
	}
	for _, l := range out.Labels {
		if l.LabelName == label {
			return l.DistinctLabelValues, nil
		}
	}
	return 0, nil
}

// SeriesCountByLabel returns the active series count per value of a label
// (Mimir cardinality API) — the basis for active-series cost attribution
// (e.g. label "service_name" → series per service).
func (c *Client) SeriesCountByLabel(ctx context.Context, label string) (map[string]int64, error) {
	var out struct {
		Labels []struct {
			LabelName   string `json:"label_name"`
			Cardinality []struct {
				LabelValue  string `json:"label_value"`
				SeriesCount int64  `json:"series_count"`
			} `json:"cardinality"`
		} `json:"labels"`
	}
	q := url.Values{}
	q.Add("label_names[]", label)
	u := fmt.Sprintf("%s/prometheus/api/v1/cardinality/label_values?%s", c.base, q.Encode())
	if err := c.get(ctx, u, &out); err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for _, l := range out.Labels {
		if l.LabelName != label {
			continue
		}
		for _, v := range l.Cardinality {
			counts[v.LabelValue] = v.SeriesCount
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
	return json.NewDecoder(resp.Body).Decode(into)
}
