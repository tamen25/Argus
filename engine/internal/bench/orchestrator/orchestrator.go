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

// BaselineProbe is implemented by probes that can observe the environment
// BEFORE injection. When a probe can, the orchestrator refuses to inject until
// the fault's signature is verifiably absent.
//
// Without this, repeats are not independent. A fault's effects can outlive its
// cleanup — scenario 1's frontend keeps exporting every series the fault
// created, because its OTel SDK holds cumulative state in memory — so repeat 2
// would pass its steady-state gate on repeat 1's leftovers before its own fault
// had done anything. Checking the baseline turns that silent corruption into a
// loud, attributable failure.
//
// It is optional so that probes which cannot observe anything (AlwaysReadyProbe,
// test fakes) are not asked to vouch for a baseline they cannot see.
type BaselineProbe interface {
	Clean(ctx context.Context, sc bench.Scenario) (bool, error)
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
	// BaselineTimeout caps how long a repeat waits for the previous fault's
	// signature to clear before it may inject. Long enough to outlast
	// Prometheus's default 5m lookback, during which a series that has stopped
	// receiving samples is still counted.
	BaselineTimeout time.Duration
	// Seed is recorded for reproducibility (architecture rule 6).
	Seed int64
	// EnvDigest identifies the environment under test, recorded in the report.
	EnvDigest string
	// Condition labels the telemetry condition the environment was in for this
	// run (e.g. "degraded", "remediated"). It is only a label: the operator
	// puts the environment in that condition, and `bench report --compare`
	// groups reports by it.
	Condition string
	// Categories is the closed list of fault categories every agent is offered.
	// The category is scored by exact match, so an agent that is not shown the
	// list cannot match it. Empty means none was offered, which the report
	// states.
	Categories bench.Categories
	// Model records which model served this run and where. Without it a
	// leaderboard row cannot be reproduced.
	Model *ModelInfo
	// AgentTimeout is the cap on a single model call, recorded in the report. It
	// is not a budget the agent sees, but a run it cuts short has no diagnosis,
	// so a slow server under a short timeout reads as an agent that broke.
	AgentTimeout time.Duration
	// CleanupTimeout caps a repeat's cleanup, which runs even after the run was
	// interrupted.
	CleanupTimeout time.Duration
	// Now is injectable for tests.
	Now func() time.Time
}

// RunRecord is one attempt: what was consumed, how it was normalized, and how
// it scored. A run with no diagnosis records why rather than scoring zero.
type RunRecord struct {
	Repeat   int         `json:"repeat"`
	Started  time.Time   `json:"started"`
	Finished time.Time   `json:"finished"`
	Usage    agent.Usage `json:"usage"`
	// ToolLog is what the agent asked its tools, in order, and whether each call
	// was answered. It is what separates an agent that investigated badly from
	// one whose tools were failing under it.
	ToolLog       []agent.ToolCall `json:"tool_log,omitempty"`
	Normalization string           `json:"normalization,omitempty"`
	Score         *scoring.Result  `json:"score,omitempty"`
	Error         string           `json:"error,omitempty"`
	// BudgetExhausted distinguishes "ran out of budget" from "broke" — an
	// important difference when reading a leaderboard.
	BudgetExhausted bool `json:"budget_exhausted,omitempty"`
	// BudgetCap names the cap that ended the run: "tool calls" or "tokens".
	BudgetCap string `json:"budget_cap,omitempty"`
}

// Summary aggregates the repeats.
type Summary struct {
	Attempts        int `json:"attempts"`
	Diagnoses       int `json:"diagnoses"`
	Failures        int `json:"failures"`
	BudgetExhausted int `json:"budget_exhausted"`
	// AnswerRate is diagnoses/attempts. It must be read together with the means:
	// those average over answered runs only, so an agent that mostly declines to
	// answer and guesses well once would otherwise look flawless.
	AnswerRate float64 `json:"answer_rate"`
	// MeanScore is the overall grade — the number a leaderboard ranks on.
	MeanScore         float64 `json:"mean_score"`
	StdDevScore       float64 `json:"stddev_score"`
	MeanEntityScore   float64 `json:"mean_entity_score"`
	StdDevEntityScore float64 `json:"stddev_entity_score"`
	CategoryMatchRate float64 `json:"category_match_rate"`
	// EvidenceMissingRate is the share of answered runs that cited no telemetry
	// in a scenario that required it.
	EvidenceMissingRate float64 `json:"evidence_missing_rate"`
	MeanToolCalls       float64 `json:"mean_tool_calls"`
	MeanTokens          float64 `json:"mean_tokens"`
}

// Report is the full record of one scenario × one agent, carrying everything
// needed to reproduce it (architecture rule 6).
type Report struct {
	Scenario     string `json:"scenario"`
	ScenarioHash string `json:"scenario_hash"`
	Agent        string `json:"agent"`
	EnvDigest    string `json:"env_digest,omitempty"`
	Condition    string `json:"condition,omitempty"`
	// CategoriesOffered is the category list the agent chose from, as shown.
	CategoriesOffered []string     `json:"categories_offered,omitempty"`
	Seed              int64        `json:"seed"`
	Budget            agent.Budget `json:"budget"`
	Model             *ModelInfo   `json:"model,omitempty"`
	// AgentTimeout is Options.AgentTimeout as a Go duration ("30m0s"); empty
	// when the adapter's default was not recorded.
	AgentTimeout string      `json:"agent_timeout,omitempty"`
	Runs         []RunRecord `json:"runs"`
	Summary      Summary     `json:"summary"`
}

// ErrInterrupted is returned, wrapping the context's error, when ctx is
// cancelled before every repeat has finished. The partial report is returned
// too, but it must not be saved as a finished cell: a matrix that resumes by
// skipping existing reports would never complete it.
var ErrInterrupted = errors.New("bench run interrupted")

// Run executes the scenario against the agent Repeats times and returns the
// report. Individual run failures are recorded, not fatal: one broken attempt
// must not discard the rest of the matrix. Cancelling ctx stops the run after
// the current repeat's cleanup, and Run returns ErrInterrupted.
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
		Scenario:          sc.Metadata.Name,
		ScenarioHash:      hash,
		Agent:             ag.Name(),
		EnvDigest:         opts.EnvDigest,
		Condition:         opts.Condition,
		CategoriesOffered: opts.Categories.Names(),
		Seed:              opts.Seed,
		Budget:            opts.Budget,
		Model:             opts.Model,
	}
	if opts.AgentTimeout > 0 {
		rep.AgentTimeout = opts.AgentTimeout.String()
	}

	for i := 0; i < opts.Repeats; i++ {
		rec := runOnce(ctx, sc, ag, tools, inj, probe, opts, i)
		rep.Runs = append(rep.Runs, rec)
		if ctx.Err() != nil {
			break
		}
	}
	rep.Summary = summarize(rep.Runs)
	if err := ctx.Err(); err != nil {
		return rep, fmt.Errorf("%w after %d of %d repeats: %w", ErrInterrupted, len(rep.Runs), opts.Repeats, err)
	}
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
	// would poison every later repeat. It also runs when the run was
	// interrupted, so it gets a context that ctx's cancellation does not reach
	// (context.WithoutCancel keeps ctx's values but drops its cancellation),
	// bounded by its own timeout instead.
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opts.CleanupTimeout)
		defer cancel()
		_ = inj.Cleanup(cctx, sc)
	}()

	if err := inj.Reset(ctx, sc); err != nil {
		rec.Error = fmt.Sprintf("reset: %v", err)
		return finish()
	}
	if err := waitBaseline(ctx, probe, sc, opts); err != nil {
		rec.Error = fmt.Sprintf("baseline: %v", err)
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
		Scenario:   sc.Metadata.Name,
		Brief:      opts.Brief(sc),
		Tools:      tools,
		Budget:     opts.Budget,
		Categories: opts.Categories.Sorted(),
	})
	rec.Usage = res.Usage
	rec.ToolLog = res.Calls
	if err != nil {
		rec.Error = err.Error()
		rec.BudgetExhausted = errors.Is(err, agent.ErrBudgetExhausted)
		var be *agent.BudgetError
		if errors.As(err, &be) {
			rec.BudgetCap = be.Cap
		}
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

// waitSteady polls the probe until the fault's signature has held for the
// scenario's settle window, or the scenario's total injected duration elapses.
//
// The settle timer is a local variable, so it cannot leak into the next repeat,
// and any poll where the signature does not hold restarts it: only an
// uninterrupted hold counts as steady.
func waitSteady(ctx context.Context, probe SteadyStateProbe, sc bench.Scenario, opts Options) error {
	settle := settleWindow(sc)
	deadline := opts.Now().Add(scenarioDuration(sc))
	var heldSince time.Time
	for {
		ok, err := probe.Reached(ctx, sc)
		if err != nil {
			return err
		}
		now := opts.Now()
		if ok {
			if heldSince.IsZero() {
				heldSince = now
			}
			if !now.Before(heldSince.Add(settle)) {
				return nil
			}
		} else {
			heldSince = time.Time{}
		}
		if !now.Before(deadline) {
			return fmt.Errorf("not reached within %s", scenarioDuration(sc))
		}
		if err := sleep(ctx, opts.PollInterval); err != nil {
			return err
		}
	}
}

// waitBaseline blocks until the fault's signature is verifiably absent, so each
// repeat measures its own fault rather than the previous repeat's residue. It
// is a no-op for scenarios without a steadyState block and for probes that
// cannot observe (see BaselineProbe).
func waitBaseline(ctx context.Context, probe SteadyStateProbe, sc bench.Scenario, opts Options) error {
	bp, ok := probe.(BaselineProbe)
	if !ok || sc.Spec.SteadyState == nil {
		return nil
	}
	deadline := opts.Now().Add(opts.BaselineTimeout)
	for {
		clean, err := bp.Clean(ctx, sc)
		if err != nil {
			return err
		}
		if clean {
			return nil
		}
		if !opts.Now().Before(deadline) {
			return fmt.Errorf("environment did not return to baseline within %s: the fault's signature "+
				"(%s) was still present before injection, so this repeat could not be independent of "+
				"the last; the scenario's reset must remove the fault's effects, not only the fault",
				opts.BaselineTimeout, sc.Spec.SteadyState.Query)
		}
		if err := sleep(ctx, opts.PollInterval); err != nil {
			return err
		}
	}
}

// settleWindow is the scenario's declared settle duration, or zero.
func settleWindow(sc bench.Scenario) time.Duration {
	if sc.Spec.SteadyState == nil {
		return 0
	}
	d, err := sc.Spec.SteadyState.SettleDur()
	if err != nil {
		return 0 // unreachable: LoadScenario validates settle
	}
	return d
}

// sleep waits for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
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
	if o.BaselineTimeout <= 0 {
		o.BaselineTimeout = 10 * time.Minute
	}
	if o.CleanupTimeout <= 0 {
		// A cleanup can be a rollout restart and its wait (scenario reset
		// hooks), so this is generous.
		o.CleanupTimeout = 10 * time.Minute
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

// ModelInfo is the model a run was served by: the model id the endpoint was
// asked for, and the endpoint.
type ModelInfo struct {
	Endpoint string `json:"endpoint,omitempty"`
	Model    string `json:"model"`
}

// Summarize aggregates run records the same way a Report's own Summary is
// built. Exported so a leaderboard can pool the runs of several reports and
// recompute, rather than average their means.
func Summarize(runs []RunRecord) Summary { return summarize(runs) }

func summarize(runs []RunRecord) Summary {
	s := Summary{Attempts: len(runs)}
	var scores, entityScores []float64
	var catMatches, noEvidence, toolCalls, tokens float64
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
		scores = append(scores, r.Score.Score)
		entityScores = append(entityScores, r.Score.EntityScore)
		if r.Score.CategoryMatch {
			catMatches++
		}
		if r.Score.EvidenceMissing {
			noEvidence++
		}
	}
	if len(runs) > 0 {
		s.AnswerRate = float64(s.Diagnoses) / float64(len(runs))
		s.MeanToolCalls = toolCalls / float64(len(runs))
		s.MeanTokens = tokens / float64(len(runs))
	}
	if len(scores) > 0 {
		s.MeanScore = mean(scores)
		s.StdDevScore = stddev(scores, s.MeanScore)
		s.MeanEntityScore = mean(entityScores)
		s.StdDevEntityScore = stddev(entityScores, s.MeanEntityScore)
		s.CategoryMatchRate = catMatches / float64(len(scores))
		s.EvidenceMissingRate = noEvidence / float64(len(scores))
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
	sd := math.Sqrt(sum / float64(len(xs)))
	// Identical repeats must report exactly zero. Floating-point cancellation
	// otherwise yields values like 2.8e-17, which render as absurd precision on a
	// leaderboard and invite doubt about numbers that are in fact correct.
	if sd < 1e-12 {
		return 0
	}
	return sd
}
