package mimir

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tamen25/Argus/engine/internal/cost"
)

// SeriesSource must satisfy the cost port (structural check, test-only import
// so production mimir code stays decoupled from cost).
var _ cost.SeriesSource = SeriesSource{}

func TestLabelValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prometheus/api/v1/label/job/values" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":["otel-demo/checkout","ad"]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.LabelValues(context.Background(), "job")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "otel-demo/checkout" {
		t.Errorf("values = %v", got)
	}
}

// Fixtures follow the label-values cardinality response documented in the
// Mimir HTTP API reference: series_count_total, then per label label_name,
// label_values_count, series_count and the top values in cardinality. An
// earlier fixture invented a distinct_label_values_count field, so the client
// read a field Mimir never sends, and every measurement came back as 0.

func TestLabelCardinality(t *testing.T) {
	selector := `{__name__="requests_total", job=~"(.+/)?ad"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/prometheus/api/v1/cardinality/label_values" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := q.Get("selector"); got != selector {
			t.Errorf("selector = %s", got)
		}
		if got := q["label_names[]"]; len(got) != 1 || got[0] != "user_id" {
			t.Errorf("label_names = %v", got)
		}
		if q.Get("count_method") != "active" {
			t.Errorf("count_method = %q, want active", q.Get("count_method"))
		}
		_, _ = w.Write([]byte(`{"series_count_total":55000,"labels":[{"label_name":"user_id","label_values_count":48211,"series_count":55000,"cardinality":[]}]}`))
	}))
	defer srv.Close()

	n, err := New(srv.URL, "").LabelCardinality(context.Background(), selector, "user_id")
	if err != nil {
		t.Fatal(err)
	}
	if n != 48211 {
		t.Errorf("cardinality = %d, want 48211 (distinct values, not series)", n)
	}
}

// No series matched: Mimir does not list the label, and that is a measured
// zero. A listed label without its count is a malformed response, not zero.
func TestLabelCardinalityAbsentAndMalformed(t *testing.T) {
	for body, wantErr := range map[string]bool{
		`{"series_count_total":0,"labels":[]}`:                                          false,
		`{"series_count_total":5,"labels":[{"label_name":"user_id","series_count":5}]}`: true,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		n, err := New(srv.URL, "").LabelCardinality(context.Background(), `{__name__="m"}`, "user_id")
		srv.Close()
		if (err != nil) != wantErr || n != 0 {
			t.Errorf("%s: n = %d, err = %v, want error %v", body, n, err, wantErr)
		}
	}
}

func TestSeriesCountByLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q["label_names[]"]; len(got) != 1 || got[0] != "job" {
			t.Errorf("label_names = %v", got)
		}
		if q.Get("limit") != "500" || q.Get("count_method") != "active" {
			t.Errorf("limit = %q count_method = %q, want 500 and active", q.Get("limit"), q.Get("count_method"))
		}
		_, _ = w.Write([]byte(`{"series_count_total":12000,"labels":[{"label_name":"job","label_values_count":2,"series_count":12000,"cardinality":[{"label_value":"checkout","series_count":8000},{"label_value":"cart","series_count":4000}]}]}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "").SeriesCountByLabel(context.Background(), "job")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["checkout"] != 8000 || got["cart"] != 4000 {
		t.Errorf("series counts = %v, want checkout 8000 and cart 4000 only", got)
	}
}

// Past Mimir's 500-value limit, and for series without the label, the counts
// still add up to the tenant total, with the gap named.
func TestSeriesCountByLabelAttributesTheRemainder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"series_count_total":20000,"labels":[{"label_name":"job","label_values_count":700,"series_count":15000,"cardinality":[{"label_value":"checkout","series_count":8000},{"label_value":"cart","series_count":4000}]}]}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "").SeriesCountByLabel(context.Background(), "job")
	if err != nil {
		t.Fatal(err)
	}
	if got["(other: 698 more values)"] != 3000 || got["(no job label)"] != 5000 {
		t.Errorf("series counts = %v, want 3000 for the 698 values not listed and 5000 without the label", got)
	}
	var total int64
	for _, n := range got {
		total += n
	}
	if total != 20000 {
		t.Errorf("attributed %d series, want the tenant total 20000", total)
	}
}

func TestErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	if _, err := c.LabelValues(context.Background(), "job"); err == nil {
		t.Error("want error on 502, got nil")
	}
}
