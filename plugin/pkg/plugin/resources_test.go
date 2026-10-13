package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// fakeEngine stands in for argus-engine's HTTP API.
func fakeEngine(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/api/report", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"spec_version":"test-sha","snapshot":{"fleet_score":84.7,"services":[]}}`))
	})
	mux.HandleFunc("/api/remediation", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("rule") == "" {
			http.Error(w, "rule and service query params are required", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("service") == "ghost" {
			http.Error(w, "service not observed", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rule_id":"RES-005","formats":{"alloy.river":"// patch"}}`))
	})
	mux.HandleFunc("/api/servicegraph", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes":[{"service":"frontend","spec_score":92.5,"findings":1}],"edges":[{"source":"frontend","target":"checkout","traces":42}]}`))
	})
	mux.HandleFunc("/api/cost", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"generated_at":"2026-07-16T12:00:00Z","window":"1h0m0s","report":{"currency":"USD","lines":[],"storage":[],"total_monthly":39.34}}`))
	})
	mux.HandleFunc("/api/bench", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"reports":2,"conditions":["degraded","remediated"],"compare":"` + r.URL.Query().Get("compare") + `"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func testApp(t *testing.T, engineURL string) *App {
	t.Helper()
	inst, err := NewApp(context.Background(), backend.AppInstanceSettings{
		JSONData: []byte(`{"engineUrl":"` + engineURL + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return inst.(*App)
}

func callResource(t *testing.T, app *App, path string) *backend.CallResourceResponse {
	t.Helper()
	var got *backend.CallResourceResponse
	pathOnly, _, _ := strings.Cut(path, "?")
	err := app.CallResource(context.Background(), &backend.CallResourceRequest{
		Method: http.MethodGet,
		Path:   strings.TrimPrefix(pathOnly, "/"),
		URL:    path,
	}, backend.CallResourceResponseSenderFunc(func(res *backend.CallResourceResponse) error {
		got = res
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// /scores proxies the engine report so the browser never needs engine access.
func TestScoresResourceProxiesReport(t *testing.T) {
	app := testApp(t, fakeEngine(t).URL)
	res := callResource(t, app, "/scores")
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Status, res.Body)
	}
	var rep struct {
		SpecVersion string `json:"spec_version"`
		Snapshot    struct {
			FleetScore float64 `json:"fleet_score"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(res.Body, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.SpecVersion != "test-sha" || rep.Snapshot.FleetScore != 84.7 {
		t.Errorf("report = %+v", rep)
	}
}

// /servicegraph proxies the engine's node/edge graph for the service graph page.
func TestServiceGraphResourceProxies(t *testing.T) {
	app := testApp(t, fakeEngine(t).URL)
	res := callResource(t, app, "/servicegraph")
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Status, res.Body)
	}
	var graph struct {
		Nodes []struct {
			Service string `json:"service"`
		} `json:"nodes"`
		Edges []struct {
			Source string `json:"source"`
			Traces int64  `json:"traces"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(res.Body, &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 1 || len(graph.Edges) != 1 || graph.Edges[0].Traces != 42 {
		t.Errorf("graph = %+v", graph)
	}
}

// /cost proxies the engine showback for the Spend page.
func TestCostResourceProxies(t *testing.T) {
	app := testApp(t, fakeEngine(t).URL)
	res := callResource(t, app, "/cost")
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Status, res.Body)
	}
	var sb struct {
		Report struct {
			TotalMonthly float64 `json:"total_monthly"`
		} `json:"report"`
	}
	if err := json.Unmarshal(res.Body, &sb); err != nil {
		t.Fatal(err)
	}
	if sb.Report.TotalMonthly != 39.34 {
		t.Errorf("total = %v, want 39.34", sb.Report.TotalMonthly)
	}
}

// Engine status codes pass through untouched — a 404 for an absent finding
// must not become a 200 with an invented patch.
func TestRemediationResourcePassesStatusThrough(t *testing.T) {
	app := testApp(t, fakeEngine(t).URL)

	res := callResource(t, app, "/remediation?rule=RES-005&service=checkout")
	if res.Status != http.StatusOK || !strings.Contains(string(res.Body), "alloy.river") {
		t.Errorf("remediation = %d %s", res.Status, res.Body)
	}
	res = callResource(t, app, "/remediation?rule=RES-005&service=ghost")
	if res.Status != http.StatusNotFound {
		t.Errorf("absent finding status = %d, want 404", res.Status)
	}
}

// A dead engine is a gateway error with a plain explanation, not a hang.
func TestResourceEngineUnreachable(t *testing.T) {
	app := testApp(t, "http://127.0.0.1:1")
	res := callResource(t, app, "/scores")
	if res.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", res.Status)
	}
	// Any viewer sees this body: it must not carry the engine's address.
	if strings.Contains(string(res.Body), "127.0.0.1") {
		t.Errorf("body leaks the engine address: %s", res.Body)
	}
}

// The engine URL is checked once, when the app is configured, instead of
// failing on every request.
func TestNormalizeEngineURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                          defaultEngineURL,
		"http://engine:8080":        "http://engine:8080",
		"https://engine.example/":   "https://engine.example",
		"  http://10.0.0.1:8080/  ": "http://10.0.0.1:8080",
	} {
		if got, err := normalizeEngineURL(in); err != nil || got != want {
			t.Errorf("normalizeEngineURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"engine:8080", "ftp://engine", "http://", "file:///etc/passwd"} {
		if _, err := normalizeEngineURL(bad); err == nil {
			t.Errorf("normalizeEngineURL(%q) accepted", bad)
		}
	}
}

func TestCheckHealth(t *testing.T) {
	app := testApp(t, fakeEngine(t).URL)
	r, err := app.CheckHealth(context.Background(), nil)
	if err != nil || r.Status != backend.HealthStatusOk {
		t.Errorf("health = %+v err=%v", r, err)
	}
	dead := testApp(t, "http://127.0.0.1:1")
	r, err = dead.CheckHealth(context.Background(), nil)
	if err != nil || r.Status != backend.HealthStatusError {
		t.Errorf("dead-engine health = %+v err=%v", r, err)
	}
}

// /bench proxies the leaderboard, passing the comparison request through.
func TestBenchResourceProxiesWithQuery(t *testing.T) {
	app := testApp(t, fakeEngine(t).URL)
	res := callResource(t, app, "/bench?compare=degraded,remediated")
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Status, res.Body)
	}
	var got struct {
		Reports int    `json:"reports"`
		Compare string `json:"compare"`
	}
	if err := json.Unmarshal(res.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Reports != 2 || got.Compare != "degraded,remediated" {
		t.Errorf("proxied response = %+v, want the compare query to reach the engine", got)
	}
}

// tokenEngine requires the bearer token on /api/, as the engine does when
// ARGUS_API_TOKEN is set.
func tokenEngine(t *testing.T, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "missing or wrong API token", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/report" {
			_, _ = w.Write([]byte(`{"snapshot":{}}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func tokenApp(t *testing.T, engineURL, token string) *App {
	t.Helper()
	inst, err := NewApp(context.Background(), backend.AppInstanceSettings{
		JSONData:                []byte(`{"engineUrl":"` + engineURL + `"}`),
		DecryptedSecureJSONData: map[string]string{apiTokenKey: token},
	})
	if err != nil {
		t.Fatal(err)
	}
	return inst.(*App)
}

// The secure token setting reaches the engine on every proxied call.
func TestProxySendsTheAPIToken(t *testing.T) {
	engine := tokenEngine(t, "s3cret")
	if res := callResource(t, tokenApp(t, engine.URL, "s3cret"), "/scores"); res.Status != http.StatusOK {
		t.Errorf("with the token: status = %d, want 200", res.Status)
	}
	if res := callResource(t, testApp(t, engine.URL), "/scores"); res.Status != http.StatusUnauthorized {
		t.Errorf("without the token: status = %d, want the engine's 401 passed through", res.Status)
	}
}

// The health check says plainly when the engine rejects the token, instead of
// reporting a healthy engine that fails on every page.
func TestCheckHealthReportsARejectedToken(t *testing.T) {
	engine := tokenEngine(t, "s3cret")
	r, err := tokenApp(t, engine.URL, "s3cret").CheckHealth(context.Background(), nil)
	if err != nil || r.Status != backend.HealthStatusOk {
		t.Errorf("right token: health = %+v err=%v", r, err)
	}
	r, err = tokenApp(t, engine.URL, "wrong").CheckHealth(context.Background(), nil)
	if err != nil || r.Status != backend.HealthStatusError || !strings.Contains(r.Message, "rejected the API token") {
		t.Errorf("wrong token: health = %+v err=%v", r, err)
	}
}
