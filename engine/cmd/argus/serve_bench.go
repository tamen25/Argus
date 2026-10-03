package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tamen25/Argus/engine/internal/bench/leaderboard"
)

// serveBenchConfig holds the /api/bench wiring for serve.
type serveBenchConfig struct {
	// reportsDir holds JSON run reports, as written by
	// `argus bench run --format json --out <dir>/<name>.json`.
	reportsDir string
}

// benchResponse is what /api/bench answers. The leaderboard is always there
// when there are reports; the comparison only when ?compare= asked for one.
type benchResponse struct {
	// Reports is how many run reports the directory held.
	Reports int `json:"reports"`
	// Conditions are the telemetry-condition labels present, sorted, so the
	// UI can offer them for a comparison. "" is an unlabeled run.
	Conditions  []string                 `json:"conditions"`
	Leaderboard *leaderboard.Leaderboard `json:"leaderboard,omitempty"`
	Comparison  *leaderboard.Comparison  `json:"comparison,omitempty"`
}

// registerBenchEndpoint serves /api/bench from a directory of bench run
// reports. The directory is re-read on every request: it is the source of
// truth, a new run shows without a restart, and the reports are small.
// Unconfigured, /api/bench 404s and the plugin says how to configure it.
func registerBenchEndpoint(mux *http.ServeMux, cfg serveBenchConfig) error {
	if cfg.reportsDir == "" {
		mux.HandleFunc("/api/bench", notConfiguredBenchHandler)
		return nil
	}
	// Fail fast on a typo: a directory that does not exist at startup is a
	// configuration error, not an empty leaderboard.
	info, err := os.Stat(cfg.reportsDir)
	if err != nil {
		return fmt.Errorf("--bench-reports: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--bench-reports: %s is not a directory", cfg.reportsDir)
	}
	mux.HandleFunc("/api/bench", benchHandler(cfg.reportsDir))
	return nil
}

func notConfiguredBenchHandler(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "bench is not configured (start the engine with --bench-reports <dir>)", http.StatusNotFound)
}

func benchHandler(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var baseline, treatment string
		if compare := r.URL.Query().Get("compare"); compare != "" {
			var ok bool
			if baseline, treatment, ok = strings.Cut(compare, ","); !ok {
				http.Error(w, "compare wants two conditions: baseline,treatment", http.StatusBadRequest)
				return
			}
			for _, label := range []string{baseline, treatment} {
				if err := validCondition(label); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			}
		}

		resp := benchResponse{Conditions: []string{}}
		// An empty directory is a leaderboard with no runs yet, not an error.
		matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(matches) == 0 {
			writeJSON(w, resp)
			return
		}
		// A file that is not a run report is an error, as in `bench report`: a
		// silently dropped report would change every mean built from the rest.
		reports, err := loadRunReports([]string{dir})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp.Reports = len(reports)
		seen := map[string]bool{}
		for _, rep := range reports {
			if !seen[rep.Condition] {
				seen[rep.Condition] = true
				resp.Conditions = append(resp.Conditions, rep.Condition)
			}
		}
		sort.Strings(resp.Conditions)

		lb, err := leaderboard.Build(reports)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp.Leaderboard = &lb
		if baseline != "" {
			c, err := leaderboard.Compare(reports, baseline, treatment)
			if err != nil {
				// A condition nobody ran is the caller's request, not a fault.
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			resp.Comparison = &c
		}
		writeJSON(w, resp)
	}
}
