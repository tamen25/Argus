package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Wiring smoke for backtest run: fake Mimir sustains a condition inside the
// test incident's window — the report must show a detection with TTD, score
// the out-of-coverage incident unverifiable, and keep the caveat footer.
func TestBacktestRunCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		ts := r.URL.Query().Get("time")
		if strings.HasPrefix(q, "count(") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"18"]}]}}`))
			return
		}
		if ts >= "2026-07-16T12:03:00Z" && ts <= "2026-07-16T12:10:00Z" {
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"service":"ad"},"value":[1,"0.9"]}]}}`)
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"backtest", "run",
		"--rules", "testdata/backtest-rules.yaml",
		"--incidents", "testdata/backtest-incidents.yaml",
		"--mimir-url", srv.URL,
		"--from", "2026-07-16T12:00:00Z",
		"--to", "2026-07-16T12:15:00Z",
		"--step", "1m",
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"# Argus Backtest",
		"## HighErr",
		"| inc-ad-errors | 3m0s |", // fired 12:05 (for:2m from 12:03), incident starts 12:02
		"unverifiable (no telemetry coverage): inc-outside", // incident beyond the window
		"Fidelity caveats",
		"Replay is not re-execution",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}
