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
