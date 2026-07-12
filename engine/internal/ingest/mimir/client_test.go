package mimir

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestLabelCardinality(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prometheus/api/v1/cardinality/label_values" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("selector"); got != `{__name__="requests_total"}` {
			t.Errorf("selector = %s", got)
		}
		if got := r.URL.Query()["label_names[]"]; len(got) != 1 || got[0] != "user_id" {
			t.Errorf("label_names = %v", got)
		}
		_, _ = w.Write([]byte(`{"series_count_total":55000,"labels":[{"label_name":"user_id","series_count":55000,"distinct_label_values_count":48211}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	n, err := c.LabelCardinality(context.Background(), "requests_total", "user_id")
	if err != nil {
		t.Fatal(err)
	}
	if n != 48211 {
		t.Errorf("cardinality = %d, want 48211 (distinct values, not series)", n)
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
