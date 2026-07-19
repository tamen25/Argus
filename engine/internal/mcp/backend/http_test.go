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
