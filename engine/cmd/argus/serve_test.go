package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/ingest"
	"github.com/tamen25/Argus/engine/internal/report"
	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/rules/builtin"

	"go.opentelemetry.io/collector/pdata/ptrace"
)

func testAPIState(t *testing.T) (*http.ServeMux, *ingest.Pipeline) {
	t.Helper()
	rs, err := builtin.Load()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := rules.NewEngine(rs)
	if err != nil {
		t.Fatal(err)
	}
	col := rules.NewCollector(eng)
	pipe := ingest.NewPipeline(col, ingest.TrackerOpts{})
	mux := http.NewServeMux()
	registerAPI(mux, col, pipe, rs, "test-spec-sha", time.Minute)
	return mux, pipe
}

func consumeSpan(p *ingest.Pipeline, svc string) {
	td := ptrace.NewTraces()
	rsp := td.ResourceSpans().AppendEmpty()
	rsp.Resource().Attributes().PutStr("service.name", svc)
	rsp.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("op")
	p.ConsumeTraces(td)
}

// /api/report serves the same honest envelope the score CLI writes, from the
// live collector state — the soak harness snapshots it hourly.
func TestAPIReport(t *testing.T) {
	mux, pipe := testAPIState(t)
	consumeSpan(pipe, "checkout")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/report", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var rep report.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if rep.SpecVersion != "test-spec-sha" || rep.RuleSetComplete {
		t.Errorf("envelope = %+v", rep)
	}
	if rep.Snapshot == nil || rep.Snapshot.Service("checkout") == nil {
		t.Errorf("snapshot missing checkout: %+v", rep.Snapshot)
	}
}

// /api/remediation renders the patch for a live finding — the plugin's
// remediation panel content.
func TestAPIRemediation(t *testing.T) {
	mux, pipe := testAPIState(t)
	consumeSpan(pipe, "") // no service.name -> RES-005 finding on <unknown>

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/remediation?rule=RES-005&service="+url.QueryEscape("<unknown>"), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		RuleID   string            `json:"rule_id"`
		Service  string            `json:"service"`
		Template string            `json:"template"`
		Formats  map[string]string `json:"formats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Template != "missing-service-name" || len(got.Formats) != 2 {
		t.Errorf("remediation = %+v", got)
	}
	for _, out := range got.Formats {
		if !strings.Contains(out, "review before applying") {
			t.Error("rendered patch missing human-review notice")
		}
	}

	// no such finding -> 404, not an invented patch
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/remediation?rule=RES-005&service=checkout", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status for absent finding = %d, want 404", rec.Code)
	}
}

func consumeLinkedSpan(p *ingest.Pipeline, svc string, traceID, spanID, parentID byte) {
	td := ptrace.NewTraces()
	rsp := td.ResourceSpans().AppendEmpty()
	rsp.Resource().Attributes().PutStr("service.name", svc)
	sp := rsp.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	sp.SetName("op")
	sp.SetTraceID([16]byte{traceID})
	sp.SetSpanID([8]byte{spanID})
	if parentID != 0 {
		sp.SetParentSpanID([8]byte{parentID})
	}
	p.ConsumeTraces(td)
}

// /api/servicegraph joins caller→callee trace edges with the latest scores —
// the plugin's service graph page. Read-only, like every /api endpoint.
func TestAPIServiceGraph(t *testing.T) {
	rs, err := builtin.Load()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := rules.NewEngine(rs)
	if err != nil {
		t.Fatal(err)
	}
	col := rules.NewCollector(eng)
	// controllable clock: edges come from the completed trace generation
	now := time.Unix(1_700_000_000, 0)
	pipe := ingest.NewPipeline(col, ingest.TrackerOpts{Window: time.Minute, Now: func() time.Time { return now }})
	mux := http.NewServeMux()
	registerAPI(mux, col, pipe, rs, "test-spec-sha", time.Minute)

	// one distributed trace: frontend root + client span, checkout server
	// span whose parent is frontend's client span
	consumeLinkedSpan(pipe, "frontend", 1, 1, 0)
	consumeLinkedSpan(pipe, "frontend", 1, 2, 1)
	consumeLinkedSpan(pipe, "checkout", 1, 3, 2)
	// past the window but under 2× — the generation completes instead of
	// aging out entirely
	now = now.Add(90 * time.Second)

	var got struct {
		Window string `json:"window"`
		Nodes  []struct {
			Service   string   `json:"service"`
			SpecScore *float64 `json:"spec_score"`
			Findings  int      `json:"findings"`
		} `json:"nodes"`
		Edges []struct {
			Source string `json:"source"`
			Target string `json:"target"`
			Traces int64  `json:"traces"`
		} `json:"edges"`
	}
	// twice: repeated scrapes must not change collector state or the answer
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/servicegraph", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("bad JSON: %v", err)
		}
	}

	if len(got.Edges) != 1 || got.Edges[0].Source != "frontend" || got.Edges[0].Target != "checkout" || got.Edges[0].Traces != 1 {
		t.Errorf("edges = %+v, want frontend→checkout ×1", got.Edges)
	}
	if got.Window != time.Minute.String() {
		t.Errorf("window = %q", got.Window)
	}
	seen := map[string]bool{}
	for _, n := range got.Nodes {
		seen[n.Service] = true
		if n.SpecScore == nil {
			t.Errorf("node %s has no spec_score despite being scored", n.Service)
		}
	}
	if !seen["frontend"] || !seen["checkout"] {
		t.Errorf("nodes = %+v, want frontend and checkout", got.Nodes)
	}
}

// /api/aggregates exposes raw aggregate rows (the calibrate input); repeated
// scrapes must not change collector state.
func TestAPIAggregates(t *testing.T) {
	mux, pipe := testAPIState(t)
	consumeSpan(pipe, "checkout")

	var rows []rules.AggregateRow
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/aggregates", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		rows = rows[:0]
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatalf("bad JSON: %v", err)
		}
	}
	found := false
	for _, r := range rows {
		if r.Service == "checkout" && r.Aggregate == "span_name_cardinality" {
			found = true
		}
	}
	if !found {
		t.Errorf("span_name_cardinality row missing: %+v", rows)
	}
}
