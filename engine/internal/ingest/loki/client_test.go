package loki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/cost"
)

// Client must satisfy the cost port (test-only import keeps production loki
// code decoupled from cost).
var _ cost.LogBytesSource = (*Client)(nil)

func TestLogBytesByService(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query" {
			t.Errorf("path = %s", r.URL.Path)
		}
		// the LogQL sums bytes per service over the window
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, "bytes_over_time") || !strings.Contains(q, "service_name") {
			t.Errorf("query = %s", q)
		}
		if !strings.Contains(q, "[1h]") {
			t.Errorf("window not in query: %s", q)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"service_name":"checkout"},"value":[1700000000,"3000000000"]},
			{"metric":{"service_name":"cart"},"value":[1700000000,"500000000"]}
		]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.LogBytesByService(context.Background(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got["checkout"] != 3_000_000_000 || got["cart"] != 500_000_000 {
		t.Errorf("bytes = %v, want checkout 3e9 cart 5e8", got)
	}
}

func TestErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	if _, err := c.LogBytesByService(context.Background(), time.Hour); err == nil {
		t.Error("want error on 500")
	}
}

// A window renders as the coarsest whole LogQL unit that fits it.
func TestLokiDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		2 * time.Hour:    "2h",
		90 * time.Minute: "90m",
		45 * time.Second: "45s",
	} {
		if got := lokiDuration(d); got != want {
			t.Errorf("lokiDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

// WithServiceLabel attributes by another stream label, and the tenant header
// is sent when set. Streams without the label are skipped.
func TestServiceLabelAndTenant(t *testing.T) {
	var tenant, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, query = r.Header.Get("X-Scope-OrgID"), r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[
			{"metric":{"app":"checkout"},"value":[1700000000,"100"]},
			{"metric":{},"value":[1700000000,"999"]}
		]}}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "team-a").WithServiceLabel("app").LogBytesByService(context.Background(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if tenant != "team-a" || !strings.Contains(query, "sum by (app)") {
		t.Errorf("tenant = %q, query = %q", tenant, query)
	}
	if len(got) != 1 || got["checkout"] != 100 {
		t.Errorf("bytes = %v, want only checkout=100", got)
	}
}

// A sample that is not a number is an error, not a silent zero.
func TestMalformedValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{"service_name":"x"},"value":[0,"lots"]}]}}`))
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "").LogBytesByService(context.Background(), time.Hour); err == nil {
		t.Error("a non-numeric sample was accepted")
	}
}
