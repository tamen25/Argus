package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func benchServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	if err := registerBenchEndpoint(mux, serveBenchConfig{reportsDir: dir}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func getBench(t *testing.T, url string) (int, benchResponse, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out benchResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("not a bench response: %v\n%s", err, body)
		}
	}
	return resp.StatusCode, out, string(body)
}

// The page reads exactly what `bench run` writes: two runs under two
// conditions give a leaderboard per condition, and a comparison on request.
func TestBenchEndpoint_ServesLeaderboardAndComparison(t *testing.T) {
	chat := chatWithSubmit(t)
	defer chat.Close()
	mimir := mimirStub(t)
	defer mimir.Close()
	dir := t.TempDir()
	runBench(t, dir, "degraded", chat.URL, mimir.URL)
	runBench(t, dir, "remediated", chat.URL, mimir.URL)
	srv := benchServer(t, dir)

	code, got, raw := getBench(t, srv.URL+"/api/bench")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, raw)
	}
	if got.Reports != 2 || strings.Join(got.Conditions, ",") != "degraded,remediated" {
		t.Errorf("reports %d, conditions %v", got.Reports, got.Conditions)
	}
	if got.Leaderboard == nil || len(got.Leaderboard.Boards) != 2 {
		t.Fatalf("leaderboard = %+v, want one board per condition", got.Leaderboard)
	}
	if got.Comparison != nil {
		t.Error("a comparison was served without being asked for")
	}
	if len(got.Leaderboard.Caveats) == 0 {
		t.Error("the leaderboard came without its caveats")
	}

	code, got, raw = getBench(t, srv.URL+"/api/bench?compare=degraded,remediated")
	if code != http.StatusOK || got.Comparison == nil {
		t.Fatalf("status %d, comparison %v: %s", code, got.Comparison, raw)
	}
	if got.Comparison.Baseline != "degraded" || got.Comparison.Treatment != "remediated" || len(got.Comparison.Agents) != 1 {
		t.Errorf("comparison = %+v", got.Comparison)
	}
}

// A new run shows on the next request, without a restart.
func TestBenchEndpoint_RereadsTheDirectory(t *testing.T) {
	chat := chatWithSubmit(t)
	defer chat.Close()
	mimir := mimirStub(t)
	defer mimir.Close()
	dir := t.TempDir()
	srv := benchServer(t, dir)

	// No runs yet is an empty leaderboard, not an error.
	code, got, raw := getBench(t, srv.URL+"/api/bench")
	if code != http.StatusOK || got.Reports != 0 || got.Leaderboard != nil || got.Conditions == nil {
		t.Fatalf("empty directory: status %d, %+v (%s)", code, got, raw)
	}

	runBench(t, dir, "degraded", chat.URL, mimir.URL)
	if _, got, _ = getBench(t, srv.URL+"/api/bench"); got.Reports != 1 {
		t.Errorf("after one run: %d reports, want 1", got.Reports)
	}
}

func TestBenchEndpoint_RefusesWhatItCannotServe(t *testing.T) {
	dir := t.TempDir()
	srv := benchServer(t, dir)

	for _, q := range []string{"compare=degraded", "compare=Degraded,remediated", "compare=a,a b"} {
		if code, _, _ := getBench(t, srv.URL+"/api/bench?"+strings.ReplaceAll(q, " ", "%20")); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, code)
		}
	}

	// A file that is not a run report is an error, never a silently dropped row.
	if err := os.WriteFile(filepath.Join(dir, "x.json"), []byte(`{"baseline":"degraded"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, raw := getBench(t, srv.URL+"/api/bench"); code != http.StatusInternalServerError ||
		!strings.Contains(raw, "not a bench run report") {
		t.Errorf("status %d (%s), want 500 naming the bad file", code, raw)
	}
}

func TestBenchEndpoint_Configuration(t *testing.T) {
	// Unconfigured: 404, with the flag to set.
	mux := http.NewServeMux()
	if err := registerBenchEndpoint(mux, serveBenchConfig{}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	code, _, raw := getBench(t, srv.URL+"/api/bench")
	if code != http.StatusNotFound || !strings.Contains(raw, "--bench-reports") {
		t.Errorf("unconfigured: status %d (%s)", code, raw)
	}

	// A directory that does not exist is a startup error, not an empty page.
	if err := registerBenchEndpoint(http.NewServeMux(), serveBenchConfig{reportsDir: filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Error("a missing directory was accepted")
	}
	file := filepath.Join(t.TempDir(), "f.json")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := registerBenchEndpoint(http.NewServeMux(), serveBenchConfig{reportsDir: file}); err == nil {
		t.Error("a file was accepted as the reports directory")
	}
}
