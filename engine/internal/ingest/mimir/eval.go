package mimir

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Query runs an instant query at time t and returns the resulting vector as
// canonical-label-set key → value. Keys are `{k="v",...}` with keys sorted
// (metric name label included as-is) so replay state tracking is stable
// across evaluations.
func (c *Client) Query(ctx context.Context, expr string, t time.Time) (map[string]float64, error) {
	var out struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	q := url.Values{}
	q.Set("query", expr)
	q.Set("time", t.UTC().Format(time.RFC3339))
	u := fmt.Sprintf("%s/prometheus/api/v1/query?%s", c.base, q.Encode())
	if err := c.get(ctx, u, &out); err != nil {
		return nil, err
	}
	if out.Status != "success" {
		return nil, fmt.Errorf("mimir query: status %q", out.Status)
	}
	if out.Data.ResultType != "vector" {
		return nil, fmt.Errorf("mimir query: result type %q, want vector (replay evaluates alert conditions)", out.Data.ResultType)
	}
	vals := make(map[string]float64, len(out.Data.Result))
	for _, r := range out.Data.Result {
		s, ok := r.Value[1].(string)
		if !ok {
			return nil, fmt.Errorf("mimir query: malformed sample value %v", r.Value[1])
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("mimir query: parsing sample %q: %w", s, err)
		}
		vals[labelKey(r.Metric)] = v
	}
	return vals, nil
}

// labelKey renders a metric's labels as a canonical sorted string.
func labelKey(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%q", k, m[k])
	}
	b.WriteByte('}')
	return b.String()
}

// EvalSource adapts a Client to the backtest replay port. Kept here so
// backtest/ never imports a concrete client (architecture rule 1); the method
// set satisfies the port structurally.
type EvalSource struct {
	Client *Client
}

// Eval satisfies backtest.EvalQuerier.
func (e EvalSource) Eval(ctx context.Context, expr string, t time.Time) (map[string]float64, error) {
	return e.Client.Query(ctx, expr, t)
}

// PresenceSource adapts a Client to the backtest presence-probe port: does
// the probe expression return any series at time t?
type PresenceSource struct {
	Client *Client
	Expr   string // e.g. count(target_info)
}

// HasData satisfies backtest.InstantQuerier.
func (p PresenceSource) HasData(ctx context.Context, t time.Time) (bool, error) {
	vals, err := p.Client.Query(ctx, p.Expr, t)
	if err != nil {
		return false, err
	}
	return len(vals) > 0, nil
}
