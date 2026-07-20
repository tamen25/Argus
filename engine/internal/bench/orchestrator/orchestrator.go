// Package orchestrator runs bench scenarios end to end: reset the environment,
// inject the fault, wait for a steady failure state, hand the agent the brief
// and read-only tools, normalize its answer, score it against ground truth, and
// repeat for variance.
//
// It is the edge coordinator: it may talk to agents (which call models), but it
// only ever hands bench/scoring a normalized Diagnosis, so the grade itself
// stays deterministic (architecture rule 2).
package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/agent"
	"github.com/tamen25/Argus/engine/internal/bench/scoring"
)

// Injector applies and removes a scenario's faults in the target environment.
// Concrete implementations (Chaos Mesh, kubectl, script) are adapter packages;
// unit tests use fakes.
type Injector interface {
	Reset(ctx context.Context, sc bench.Scenario) error
	Inject(ctx context.Context, sc bench.Scenario, step bench.InjectStep) error
	Cleanup(ctx context.Context, sc bench.Scenario) error
}

// SteadyStateProbe reports whether the environment has reached the stable
// failure state worth handing to an agent. It lets a scenario end early instead
// of always burning its full duration (run-matrix economics, §3.2).
type SteadyStateProbe interface {
	Reached(ctx context.Context, sc bench.Scenario) (bool, error)
}

// Options configure a run.
type Options struct {
	// Repeats is how many times the scenario runs against the agent (variance).
	Repeats int
	// Budget caps each individual run.
	Budget agent.Budget
	// Normalizers are tried in order; the first success wins and its Method() is
	// recorded. Put the deterministic bench.JSONNormalizer first and any
	// LLM-judge last, so a run only reports non-deterministic normalization when
	// it actually needed it.
	Normalizers []bench.Normalizer
	// Brief builds the incident brief handed to the agent. The default never
	// mentions ground truth — an agent must find the root cause, not be told it.
	Brief func(bench.Scenario) string
	// PollInterval is how often the steady-state probe is checked.
	PollInterval time.Duration
	// Seed is recorded for reproducibility (architecture rule 6).
	Seed int64
	// EnvDigest identifies the environment under test, recorded in the report.
	EnvDigest string
	// Now is injectable for tests.
	Now func() time.Time
}

// RunRecord is one attempt: what was consumed, how it was normalized, and how
// it scored. A run with no diagnosis records why rather than scoring zero.
type RunRecord struct {
	Repeat        int             `json:"repeat"`
	Started       time.Time       `json:"started"`
	Finished      time.Time       `json:"finished"`
	Usage         agent.Usage     `json:"usage"`
	Normalization string          `json:"normalization,omitempty"`
	Score         *scoring.Result `json:"score,omitempty"`
	Error         string          `json:"error,omitempty"`
	// BudgetExhausted distinguishes "ran out of budget" from "broke" — an
	// important difference when reading a leaderboard.
	BudgetExhausted bool `json:"budget_exhausted,omitempty"`
}

// Summary aggregates the repeats.
type Summary struct {
	Attempts          int     `json:"attempts"`
	Diagnoses         int     `json:"diagnoses"`
	Failures          int     `json:"failures"`
	BudgetExhausted   int     `json:"budget_exhausted"`
	MeanEntityScore   float64 `json:"mean_entity_score"`
	StdDevEntityScore float64 `json:"stddev_entity_score"`
	CategoryMatchRate float64 `json:"category_match_rate"`
	MeanToolCalls     float64 `json:"mean_tool_calls"`
	MeanTokens        float64 `json:"mean_tokens"`
}

// Report is the full record of one scenario × one agent, carrying everything
// needed to reproduce it (architecture rule 6).
type Report struct {
	Scenario     string       `json:"scenario"`
	ScenarioHash string       `json:"scenario_hash"`
	Agent        string       `json:"agent"`
	EnvDigest    string       `json:"env_digest,omitempty"`
	Seed         int64        `json:"seed"`
	Budget       agent.Budget `json:"budget"`
	Runs         []RunRecord  `json:"runs"`
	Summary      Summary      `json:"summary"`
}

// Run executes the scenario against the agent Repeats times and returns the
// report. Individual run failures are recorded, not fatal: one broken attempt
// must not discard the rest of the matrix.
func Run(
	ctx context.Context,
	sc bench.Scenario,
	ag agent.Agent,
	tools agent.Tools,
	inj Injector,
	probe SteadyStateProbe,
	opts Options,
) (Report, error) {
	opts = withDefaults(opts)
	hash, err := HashScenario(sc)
	if err != nil {
		return Report{}, err
	}

	rep := Report{
		Scenario:     sc.Metadata.Name,
		ScenarioHash: hash,
		Agent:        ag.Name(),
		EnvDigest:    opts.EnvDigest,
		Seed:         opts.Seed,
		Budget:       opts.Budget,
	}

	for i := 0; i < opts.Repeats; i++ {
		rec := runOnce(ctx, sc, ag, tools, inj, probe, opts, i)
		rep.Runs = append(rep.Runs, rec)
		if ctx.Err() != nil {
			break
		}
	}
	rep.Summary = summarize(rep.Runs)
	return rep, nil
}

func runOnce(
	ctx context.Context,
	sc bench.Scenario,
	ag agent.Agent,
	tools agent.Tools,
	inj Injector,
	probe SteadyStateProbe,
	opts Options,
	repeat int,
) RunRecord {
	rec := RunRecord{Repeat: repeat, Started: opts.Now()}
	finish := func() RunRecord {
		rec.Finished = opts.Now()
		return rec
	}

	// Cleanup always runs, even when the attempt fails partway: a leaked fault
	// would poison every later repeat.
	defer func() { _ = inj.Cleanup(ctx, sc) }()

	if err := inj.Reset(ctx, sc); err != nil {
		rec.Error = fmt.Sprintf("reset: %v", err)
		return finish()
	}
	for _, step := range sc.Spec.Inject {
		if err := inj.Inject(ctx, sc, step); err != nil {
			rec.Error = fmt.Sprintf("inject: %v", err)
			return finish()
		}
	}
	if err := waitSteady(ctx, probe, sc, opts); err != nil {
		rec.Error = fmt.Sprintf("steady state: %v", err)
		return finish()
	}

	res, err := ag.Diagnose(ctx, agent.Task{
		Scenario: sc.Metadata.Name,
		Brief:    opts.Brief(sc),
		Tools:    tools,
		Budget:   opts.Budget,
	})
	rec.Usage = res.Usage
	if err != nil {
		rec.Error = err.Error()
		rec.BudgetExhausted = errors.Is(err, agent.ErrBudgetExhausted)
		return finish()
	}

	d, method, err := normalize(ctx, opts.Normalizers, res.Raw, sc.Metadata.Name)
	rec.Normalization = method
	if err != nil {
		rec.Error = fmt.Sprintf("normalize: %v", err)
		return finish()
	}

	s := scoring.Score(sc.Spec.GroundTruth, sc.Spec.Scoring, d)
	rec.Score = &s
	return finish()
}

// normalize tries each normalizer in order, returning the first success and the
// method that produced it. The method is recorded so a report always discloses
// whether a non-deterministic step was involved (architecture rule 7).
func normalize(ctx context.Context, ns []bench.Normalizer, raw json.RawMessage, scenario string) (bench.Diagnosis, string, error) {
	var lastErr error
	for _, n := range ns {
		d, err := n.Normalize(ctx, raw, scenario)
		if err == nil {
			return d, n.Method(), nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no normalizers configured")
	}
	return bench.Diagnosis{}, "", lastErr
}

// waitSteady polls the probe until it reports a steady failure state or the
// scenario's total injected duration elapses.
func waitSteady(ctx context.Context, probe SteadyStateProbe, sc bench.Scenario, opts Options) error {
	deadline := opts.Now().Add(scenarioDuration(sc))
	for {
		ok, err := probe.Reached(ctx, sc)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if !opts.Now().Before(deadline) {
			return fmt.Errorf("not reached within %s", scenarioDuration(sc))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(opts.PollInterval):
		}
	}
}

// scenarioDuration is the sum of the inject steps' durations — the window in
// which the environment is expected to reach its failure state.
func scenarioDuration(sc bench.Scenario) time.Duration {
	var total time.Duration
	for _, s := range sc.Spec.Inject {
		if d, err := s.Dur(); err == nil {
			total += d
		}
	}
	if total <= 0 {
		total = 10 * time.Minute
	}
	return total
}

// HashScenario returns a stable digest of the scenario, recorded in every report
// so a result can be tied to the exact scenario definition that produced it.
func HashScenario(sc bench.Scenario) (string, error) {
	b, err := json.Marshal(sc)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// DefaultBrief is the incident brief handed to an agent when none is supplied.
// It deliberately contains NO ground truth: the agent must find the root cause
// from telemetry, not read it in the prompt.
func DefaultBrief(sc bench.Scenario) string {
	return fmt.Sprintf(
		"An incident is affecting the %q environment. Investigate using the available "+
			"read-only observability tools (metrics, logs, traces, alerts, topology) and "+
			"identify which Kubernetes entities are the root cause and what kind of fault it is.",
		sc.Spec.Environment.App)
}

func withDefaults(o Options) Options {
	if o.Repeats <= 0 {
		o.Repeats = 1
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 10 * time.Second
	}
	if o.Brief == nil {
		o.Brief = DefaultBrief
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if len(o.Normalizers) == 0 {
		o.Normalizers = []bench.Normalizer{bench.JSONNormalizer{}}
	}
	return o
}

func summarize(runs []RunRecord) Summary {
	s := Summary{Attempts: len(runs)}
	var scores []float64
	var catMatches, toolCalls, tokens float64
	for _, r := range runs {
		toolCalls += float64(r.Usage.ToolCalls)
		tokens += float64(r.Usage.Tokens)
		if r.BudgetExhausted {
			s.BudgetExhausted++
		}
		if r.Score == nil {
			s.Failures++
			continue
		}
		s.Diagnoses++
		scores = append(scores, r.Score.EntityScore)
		if r.Score.CategoryMatch {
			catMatches++
		}
	}
	if len(runs) > 0 {
		s.MeanToolCalls = toolCalls / float64(len(runs))
		s.MeanTokens = tokens / float64(len(runs))
	}
	if len(scores) > 0 {
		s.MeanEntityScore = mean(scores)
		s.StdDevEntityScore = stddev(scores, s.MeanEntityScore)
		s.CategoryMatchRate = catMatches / float64(len(scores))
	}
	return s
}

func mean(xs []float64) float64 {
	var t float64
	for _, x := range xs {
		t += x
	}
	return t / float64(len(xs))
}

// stddev is the population standard deviation across repeats — the variance
// number a leaderboard must show next to any mean.
func stddev(xs []float64, m float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		d := x - m
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(xs)))
}
