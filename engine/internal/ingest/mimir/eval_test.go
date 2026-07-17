package mimir

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// The eval adapters must satisfy the backtest ports (structural check,
// test-only import so production mimir code stays decoupled from backtest).
var (
	_ backtest.EvalQuerier    = EvalSource{}
	_ backtest.InstantQuerier = PresenceSource{}
)

func TestQueryInstantVector(t *testing.T) {
	at := time.Date(2026, 7, 16, 21, 25, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prometheus/api/v1/query" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("query"); got != `up == 0` {
			t.Errorf("query = %q", got)
		}
		if got := r.URL.Query().Get("time"); got != "2026-07-16T21:25:00Z" {
			t.Errorf("time = %q", got)
		}
		if got := r.Header.Get("X-Scope-OrgID"); got != "anonymous" {
			t.Errorf("tenant = %q", got)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"service":"ad","job":"x"},"value":[1752700000,"0.07"]},
			{"metric":{"service":"cart"},"value":[1752700000,"0.01"]}]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "anonymous")
	got, err := c.Query(context.Background(), `up == 0`, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("series = %v, want 2", got)
	}
	// keys are canonical sorted label sets so replay state tracking is stable
	if got[`{job="x",service="ad"}`] != 0.07 {
		t.Errorf("ad value missing: %v", got)
	}
	if got[`{service="cart"}`] != 0.01 {
		t.Errorf("cart value missing: %v", got)
	}
}

func TestQueryRejectsNonVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[1752700000,"1"]}}`))
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "").Query(context.Background(), `1`, time.Now()); err == nil {
		t.Error("scalar result accepted, want error (replay needs vectors)")
	}
}

func TestPresenceSource(t *testing.T) {
	empty := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if empty {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"18"]}]}}`))
	}))
	defer srv.Close()

	p := PresenceSource{Client: New(srv.URL, ""), Expr: "count(target_info)"}
	ok, err := p.HasData(context.Background(), time.Now())
	if err != nil || !ok {
		t.Errorf("HasData = %v, %v — want true", ok, err)
	}
	empty = true
	ok, err = p.HasData(context.Background(), time.Now())
	if err != nil || ok {
		t.Errorf("HasData = %v, %v — want false", ok, err)
	}
}
