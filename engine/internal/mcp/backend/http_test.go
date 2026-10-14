package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMimir_QueryInstant_PassthroughAndTenant(t *testing.T) {
	var gotPath, gotTenant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		gotTenant = r.Header.Get("X-Scope-OrgID")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	m := NewMimir(srv.URL, "team-a")
	raw, err := m.QueryInstant(context.Background(), "up", time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) || !strings.Contains(string(raw), `"success"`) {
		t.Errorf("body not passed through: %s", raw)
	}
	if !strings.HasPrefix(gotPath, "/prometheus/api/v1/query?") || !strings.Contains(gotPath, "query=up") {
		t.Errorf("path = %s", gotPath)
	}
	if gotTenant != "team-a" {
		t.Errorf("tenant header = %q", gotTenant)
	}
}

func TestMimir_QueryRange_StepSeconds(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	m := NewMimir(srv.URL, "")
	_, err := m.QueryRange(context.Background(), "up", time.Unix(0, 0), time.Unix(3600, 0), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQuery, "step=30") {
		t.Errorf("step not encoded as seconds: %s", gotQuery)
	}
}

func TestMimir_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`bad query`))
	}))
	defer srv.Close()

	m := NewMimir(srv.URL, "")
	if _, err := m.QueryInstant(context.Background(), "((", time.Now()); err == nil {
		t.Error("expected error on HTTP 400")
	}
}

func TestMimir_ListAlerts_FilterByState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"alerts":[
			{"labels":{"alertname":"A"},"state":"firing"},
			{"labels":{"alertname":"B"},"state":"pending"}
		]}}`))
	}))
	defer srv.Close()

	m := NewMimir(srv.URL, "")
	raw, err := m.ListAlerts(context.Background(), "firing")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Data struct {
			Alerts []struct {
				State string `json:"state"`
			} `json:"alerts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Data.Alerts) != 1 || doc.Data.Alerts[0].State != "firing" {
		t.Errorf("filter failed: %s", raw)
	}
}

func TestMimir_ListAlerts_NoStateReturnsAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"alerts":[{"state":"firing"},{"state":"pending"}]}}`))
	}))
	defer srv.Close()

	raw, err := NewMimir(srv.URL, "").ListAlerts(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"state"`) != 2 {
		t.Errorf("expected all alerts passed through: %s", raw)
	}
}

func TestLokiAndTempo_Paths(t *testing.T) {
	var lokiPath, tempoPath string
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lokiPath = r.URL.Path + "?" + r.URL.RawQuery
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer loki.Close()
	tempo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tempoPath = r.URL.Path + "?" + r.URL.RawQuery
		_, _ = w.Write([]byte(`{"traces":[]}`))
	}))
	defer tempo.Close()

	if _, err := NewLoki(loki.URL, "").QueryRange(context.Background(), `{app="x"}`, time.Unix(0, 0), time.Unix(60, 0), 50); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lokiPath, "/loki/api/v1/query_range?") || !strings.Contains(lokiPath, "limit=50") {
		t.Errorf("loki path = %s", lokiPath)
	}
	if _, err := NewTempo(tempo.URL, "").Search(context.Background(), `{}`, 20); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tempoPath, "/api/search?") || !strings.Contains(tempoPath, "limit=20") {
		t.Errorf("tempo path = %s", tempoPath)
	}
}

// An agent-supplied name is one path segment, never a way to another endpoint.
func TestPathSegment(t *testing.T) {
	for _, bad := range []string{"", ".", ".."} {
		if _, err := pathSegment(bad); err == nil {
			t.Errorf("pathSegment(%q) accepted", bad)
		}
	}
	for in, want := range map[string]string{
		"job":                   "job",
		"resource.service.name": "resource.service.name",
		"../x":                  "..%2Fx", // the slash is escaped: still one segment
	} {
		if got, err := pathSegment(in); err != nil || got != want {
			t.Errorf("pathSegment(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// R29: an answer over the cap is an error that tells the agent how to narrow
// its query, not a cut-off document that looks complete.
func TestGetRawRefusesOversizedAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxToolAnswer+1)))
	}))
	defer srv.Close()
	_, err := getRaw(context.Background(), srv.Client(), srv.URL, "")
	if err == nil || !strings.Contains(err.Error(), "narrow the query") {
		t.Errorf("err = %v, want a size error that says how to narrow the query", err)
	}
}
