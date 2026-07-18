package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

func writeBacktestFiles(t *testing.T) (rules, incidents string) {
	t.Helper()
	dir := t.TempDir()
	rules = filepath.Join(dir, "rules.yaml")
	if err := os.WriteFile(rules, []byte(`groups:
  - name: g
    rules:
      - alert: HighErr
        expr: errs > 0.05
        for: 5m
`), 0o644); err != nil {
		t.Fatal(err)
	}
	incidents = filepath.Join(dir, "incidents.yaml")
	if err := os.WriteFile(incidents, []byte(`version: 1
incidents:
  - id: inc-1
    title: t
    start: "2026-07-18T05:00:00Z"
    end:   "2026-07-18T05:10:00Z"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	return rules, incidents
}

// A fake Mimir: presence probe always has data, alert condition never fires —
// enough to exercise the endpoint's pipeline and caching.
func fakeMimir(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if strings.HasPrefix(q, "count(") {
			*calls++
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"18"]}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Configured endpoint serves a report and caches it within the TTL.
func TestBacktestEndpointCaches(t *testing.T) {
	rules, incidents := writeBacktestFiles(t)
	calls := 0
	srv := fakeMimir(t, &calls)

	mux := http.NewServeMux()
	if err := registerBacktestEndpoint(mux, serveBacktestConfig{
		rulePaths: []string{rules}, incidentsPath: incidents,
		mimirURL: srv.URL, window: time.Hour, step: 30 * time.Minute,
		grace: 5 * time.Minute, probeExpr: "count(target_info)", cacheTTL: time.Minute,
	}); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backtest", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var rep backtest.Report
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatal(err)
		}
		if len(rep.Rules) != 1 || rep.Rules[0].Rule != "HighErr" {
			t.Errorf("report rules = %+v", rep.Rules)
		}
	}
	firstCalls := calls
	if firstCalls == 0 {
		t.Fatal("probe never called")
	}
	// second and third requests were cached: probe-call count didn't grow
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backtest", nil))
	if calls != firstCalls {
		t.Errorf("probe hit again (%d → %d), want cached", firstCalls, calls)
	}
}

// Unconfigured → 404 the plugin renders as "not configured".
func TestBacktestNotConfiguredReturns404(t *testing.T) {
	mux := http.NewServeMux()
	if err := registerBacktestEndpoint(mux, serveBacktestConfig{}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backtest", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// A bad rule file fails registration at startup (fail fast), never silently.
func TestBacktestRegistrationValidatesFiles(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(bad, []byte("groups:\n  - name: g\n    rules:\n      - alert: X\n        expr: \"sum by (((\"\n"), 0o644)
	err := registerBacktestEndpoint(http.NewServeMux(), serveBacktestConfig{
		rulePaths: []string{bad}, mimirURL: "http://x", incidentsPath: "does-not-matter",
	})
	if err == nil {
		t.Error("bad rule file registered without error")
	}
}
