package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
	"github.com/tamen25/Argus/engine/internal/ingest/mimir"
)

// serveBacktestConfig holds the /api/backtest wiring for serve.
type serveBacktestConfig struct {
	rulePaths     []string
	sloPath       string
	incidentsPath string
	mimirURL      string
	mimirTenant   string
	window        time.Duration // rolling lookback: [now-window, now]
	step          time.Duration
	grace         time.Duration
	probeExpr     string
	synthesize    bool
	cacheTTL      time.Duration
}

// registerBacktestEndpoint mounts /api/backtest. Unconfigured (no rules or no
// Mimir URL) → 404 the plugin renders as "not configured". Configured → the
// rule and incident files are validated once at startup (fail fast), then the
// endpoint serves a cached rolling-window report.
func registerBacktestEndpoint(mux *http.ServeMux, cfg serveBacktestConfig) error {
	if len(cfg.rulePaths) == 0 || cfg.mimirURL == "" {
		mux.HandleFunc("/api/backtest", notConfiguredBacktestHandler)
		return nil
	}
	// fail fast: the rule and incident files must parse at startup
	if _, err := loadBacktestInputs(cfg); err != nil {
		return fmt.Errorf("backtest config: %w", err)
	}
	ep := &backtestEndpoint{cfg: cfg, now: time.Now}
	mux.HandleFunc("/api/backtest", ep.handler())
	return nil
}

// backtestEndpoint serves /api/backtest, caching the report for cacheTTL so
// the plugin's polling never re-runs the (expensive) replay on every scrape.
type backtestEndpoint struct {
	cfg serveBacktestConfig
	now func() time.Time

	mu       sync.Mutex
	cached   *backtest.Report
	cachedAt time.Time
}

// get returns a cached report within the TTL, else recomputes over the
// rolling window. Files are reloaded each recompute so editing incidents.yaml
// or the rules is reflected without a restart. Errors are not cached.
func (e *backtestEndpoint) get(ctx context.Context) (*backtest.Report, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cached != nil && e.now().Sub(e.cachedAt) < e.cfg.cacheTTL {
		return e.cached, nil
	}
	in, err := loadBacktestInputs(e.cfg)
	if err != nil {
		return nil, err
	}
	to := e.now().UTC()
	in.From = to.Add(-e.cfg.window)
	in.To = to

	client := mimir.New(e.cfg.mimirURL, e.cfg.mimirTenant)
	rep, err := backtest.Run(ctx, mimir.EvalSource{Client: client},
		mimir.PresenceSource{Client: client, Expr: e.cfg.probeExpr}, in)
	if err != nil {
		return nil, err
	}
	e.cached = &rep
	e.cachedAt = e.now()
	return e.cached, nil
}

func (e *backtestEndpoint) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep, err := e.get(r.Context())
		if err != nil {
			http.Error(w, "backtest sources unavailable: "+err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, rep)
	}
}

// loadBacktestInputs loads and validates the rule set (+ SLO burn-rate
// expansion) and incident registry — the window is filled in per request.
func loadBacktestInputs(cfg serveBacktestConfig) (backtest.RunInput, error) {
	rs, err := backtest.LoadRuleFiles(cfg.rulePaths...)
	if err != nil {
		return backtest.RunInput{}, err
	}
	if cfg.sloPath != "" {
		policies, err := backtest.LoadSLOPolicies(cfg.sloPath)
		if err != nil {
			return backtest.RunInput{}, err
		}
		for _, p := range policies {
			rs.Groups = append(rs.Groups, backtest.Group{
				Name: "slo:" + p.Name, Interval: cfg.step, Rules: backtest.BurnRateRules(p),
			})
		}
	}
	reg, err := backtest.LoadIncidents(cfg.incidentsPath)
	if err != nil {
		return backtest.RunInput{}, err
	}
	return backtest.RunInput{
		Rules: rs, Incidents: reg.Incidents,
		Step: cfg.step, Grace: cfg.grace, Synthesize: cfg.synthesize,
	}, nil
}

// notConfiguredBacktestHandler answers /api/backtest when no rules are set — a
// 404 the plugin renders as "not configured", never an empty report.
func notConfiguredBacktestHandler(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "backtest is not configured (start the engine with --backtest-rules and --backtest-mimir-url)", http.StatusNotFound)
}
