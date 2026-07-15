// Package loki is the concrete adapter for Loki's HTTP API (architecture rule
// 1: concrete clients only in adapter packages). Read-only; used for log-bytes
// cost attribution per service.
package loki

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

// Client talks to Loki's query API.
type Client struct {
	base   string // e.g. http://loki-gateway.lgtm.svc
	tenant string // X-Scope-OrgID; empty for anonymous single-tenant
	http   *http.Client
	label  string // service label, default "service_name"
}

// New builds a client for the given base URL and optional tenant.
func New(base, tenant string) *Client {
	return &Client{base: base, tenant: tenant, http: &http.Client{Timeout: 30 * time.Second}, label: "service_name"}
}

// WithServiceLabel overrides the stream label used to attribute bytes.
func (c *Client) WithServiceLabel(label string) *Client {
	c.label = label
	return c
}

// LogBytesByService returns log bytes ingested per service over the window,
// via LogQL `bytes_over_time` summed by the service label. Satisfies
// cost.LogBytesSource.
//
// The window is a lower bound on true monthly ingest (a longer window is more
// representative); the cost core extrapolates it to a monthly bill.
func (c *Client) LogBytesByService(ctx context.Context, window time.Duration) (map[string]int64, error) {
	query := fmt.Sprintf("sum by (%s) (bytes_over_time({%s=~\".+\"}[%s]))", c.label, c.label, lokiDuration(window))

	var out struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	q := url.Values{}
	q.Set("query", query)
	u := fmt.Sprintf("%s/loki/api/v1/query?%s", c.base, q.Encode())
	if err := c.get(ctx, u, &out); err != nil {
		return nil, err
	}
	if out.Status != "success" {
		return nil, fmt.Errorf("loki query: status %q", out.Status)
	}

	bytesBySvc := map[string]int64{}
	for _, r := range out.Data.Result {
		svc := r.Metric[c.label]
		if svc == "" {
			continue
		}
		s, ok := r.Value[1].(string)
		if !ok {
			continue
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("loki value %q: %w", s, err)
		}
		bytesBySvc[svc] = int64(n)
	}
	return bytesBySvc, nil
}

// lokiDuration renders a Go duration as a LogQL range like "1h" / "30m".
func lokiDuration(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
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
		return fmt.Errorf("loki %s: HTTP %d: %s", u, resp.StatusCode, b)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}
