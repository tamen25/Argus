package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// End-to-end wiring smoke: fake Mimir scripts a condition that holds long
// enough for for:2m — the command must report the firing, coverage, and the
// standing fidelity caveat.
func TestBacktestReplayCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		ts := r.URL.Query().Get("time")
		if strings.HasPrefix(q, "count(") { // presence probe: always data
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"18"]}]}}`))
			return
		}
		// alert condition: true from 12:02 through 12:08
		if ts >= "2026-07-16T12:02:00Z" && ts <= "2026-07-16T12:08:00Z" {
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"service":"ad"},"value":[1,"0.9"]}]}}`)
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"backtest", "replay",
		"--rules", "testdata/backtest-rules.yaml",
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
		"HighErr (for: 2m0s)",
		`active 2026-07-16T12:02:00Z`,
		`fired 2026-07-16T12:04:00Z`,
		`resolved 2026-07-16T12:09:00Z`,
		"Fidelity caveats",
		"staleness, lookback-delta",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}
