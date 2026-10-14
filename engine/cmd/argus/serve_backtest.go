package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
		mux.HandleFunc("GET /api/backtest", notConfiguredBacktestHandler)
		return nil
	}
	// fail fast: the rule and incident files must parse at startup
	if _, err := loadBacktestInputs(cfg); err != nil {
		return fmt.Errorf("backtest config: %w", err)
	}
	ep := &backtestEndpoint{cfg: cfg, now: time.Now}
	ep.cache = newTTLCache[backtest.Report](cfg.cacheTTL, backtestComputeTimeout)
	ep.cache.now = func() time.Time { return ep.now() }
	mux.HandleFunc("GET /api/backtest", ep.handler())
	return nil
}

// backtestEndpoint serves /api/backtest, caching the report for cacheTTL so
// the plugin's polling never re-runs the (expensive) replay on every scrape.
type backtestEndpoint struct {
	cfg   serveBacktestConfig
	now   func() time.Time
	cache *ttlCache[backtest.Report]
}

// get returns the report over the rolling window, recomputed once its cache
// entry is stale. The files are reloaded on each recompute, so editing
// incidents.yaml or the rules shows without a restart.
func (e *backtestEndpoint) get(ctx context.Context) (*backtest.Report, error) {
	return e.cache.get(ctx, func(ctx context.Context) (backtest.Report, error) {
		in, err := loadBacktestInputs(e.cfg)
		if err != nil {
			return backtest.Report{}, err
		}
		in.To = e.now().UTC()
		in.From = in.To.Add(-e.cfg.window)
		client := mimir.New(e.cfg.mimirURL, e.cfg.mimirTenant)
		return backtest.Run(ctx, mimir.EvalSource{Client: client},
			mimir.PresenceSource{Client: client, Expr: e.cfg.probeExpr}, in)
	})
}

func (e *backtestEndpoint) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), maxResponseWait)
		defer cancel()
		rep, err := e.get(ctx)
		if errors.Is(err, errStillComputing) {
			stillComputing(w, "the backtest")
			return
		}
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
	if err := addSLORules(&rs, cfg.sloPath, cfg.step); err != nil {
		return backtest.RunInput{}, err
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

// addSLORules appends the burn-rate alerting rules generated from an SLO
// policy file (if one is given) to the rule set, one group per policy.
// Burn-rate simulation is the replay pipeline over generated rules: the same
// for:-state semantics and caveats, no second evaluator.
func addSLORules(rs *backtest.RuleSet, sloPath string, step time.Duration) error {
	if sloPath == "" {
		return nil
	}
	policies, err := backtest.LoadSLOPolicies(sloPath)
	if err != nil {
		return err
	}
	for _, p := range policies {
		rs.Groups = append(rs.Groups, backtest.Group{
			Name: "slo:" + p.Name, Interval: step, Rules: backtest.BurnRateRules(p),
		})
	}
	return nil
}
