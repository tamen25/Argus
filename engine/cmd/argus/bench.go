package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/agent"
	"github.com/tamen25/Argus/engine/internal/bench/inject/kube"
	"github.com/tamen25/Argus/engine/internal/bench/judge"
	"github.com/tamen25/Argus/engine/internal/bench/local"
	"github.com/tamen25/Argus/engine/internal/bench/orchestrator"
	"github.com/tamen25/Argus/engine/internal/mcp"
	"github.com/tamen25/Argus/engine/internal/mcp/backend"
)

func newBenchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Fault-injection benchmark: can an agent diagnose incidents from this telemetry?",
	}
	cmd.AddCommand(newBenchRunCmd(), newBenchImportITBenchCmd(), newBenchReportCmd())
	return cmd
}

type benchFlags struct {
	scenario string

	agentKind    string
	endpoint     string
	model        string
	apiKeyEnv    string
	shellCommand string
	shellArgs    []string
	stubProfile  string
	stubObvious  string
	stubCategory string
	stubShotgun  []string

	mimirURL string
	lokiURL  string
	tempoURL string
	tenant   string

	repeats      int
	maxToolCalls int
	maxTokens    int
	seed         int64
	envDigest    string
	condition    string

	inject          string
	resetScript     string
	cleanupScript   string
	injectNamespace string
	kubeContext     string

	judgeEndpoint string
	judgeModel    string
	judgeKeyEnv   string

	localOnly    bool
	minContext   int
	agentTimeout time.Duration
	judgeTimeout time.Duration

	format string
	out    string
}

func newBenchRunCmd() *cobra.Command {
	var f benchFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a bench scenario against an agent and score its diagnosis",
		Long: `Runs a scenario end to end: inject the fault, hand the agent the incident
brief plus the read-only MCP tool surface, normalize its answer, and score it
against the scenario's labeled ground truth. Repeats give variance.

The agent is never told the answer: the brief names only the environment.
Scoring is deterministic — an agent's prose is recorded, never graded.

Budgets are enforced per run and printed on the report. A run that exhausts its
budget or errors is recorded as producing no diagnosis; it is NOT scored as
zero, so a crashed run cannot quietly drag an average down.

Injection modes:
  --inject=auto     (default) manifests via kubectl, scripts via bash, and the
                    scenario's own reset/cleanup hooks
  --inject=script   legacy: script steps only
  --inject=kubectl  legacy: manifest steps only
  --inject=none     inject nothing; score against an environment you already
                    put into the desired state yourself

Each injector rejects step types it cannot execute rather than skipping them,
so a scenario is never scored against an environment that was never faulted.

Pass --local-only to guarantee a run never reaches a paid API: endpoints must
then be loopback and API keys are refused outright, so the guarantee is
enforced rather than intended. Without it, any OpenAI-compatible or Anthropic
endpoint may be benchmarked.

A loopback endpoint's served context is probed before the run, and the run
aborts below --min-context: Ollama serves a model at its own small default
unless num_ctx is set explicitly, and a result produced under silent truncation
measures the context window, not the agent. The judge model must differ from the
agent model regardless of where either is hosted — a model that misreads its own
output the same way twice would launder that error into the score.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sc, err := bench.LoadScenario(f.scenario)
			if err != nil {
				return err
			}
			if f.condition != "" {
				if err := validCondition(f.condition); err != nil {
					return err
				}
			}
			// Policy first: nothing may reach an endpoint or a model until the
			// local-only rules have passed.
			if err := enforceLocalPolicy(f); err != nil {
				return err
			}
			ag, err := buildAgent(f)
			if err != nil {
				return err
			}
			model, err := probeModel(cmd.Context(), f)
			if err != nil {
				return err
			}
			if err := enforceDistinctJudgeWeights(cmd.Context(), f, model); err != nil {
				return err
			}
			tools, err := buildTools(f)
			if err != nil {
				return err
			}
			inj, err := buildInjector(f, sc)
			if err != nil {
				return err
			}

			opts := orchestrator.Options{
				Repeats:     f.repeats,
				Budget:      agent.Budget{MaxToolCalls: f.maxToolCalls, MaxTokens: f.maxTokens},
				Normalizers: buildNormalizers(f),
				Seed:        f.seed,
				EnvDigest:   f.envDigest,
				Condition:   f.condition,
				Model:       model,
			}

			rep, err := orchestrator.Run(cmd.Context(), sc, ag, tools, inj, buildProbe(f, sc), opts)
			if err != nil {
				return err
			}
			return writeBenchReport(cmd, f, rep)
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&f.scenario, "scenario", "", "scenario YAML (argus/v1alpha1 BenchScenario)")
	fl.StringVar(&f.agentKind, "agent", "openai", "agent adapter: openai | anthropic | shell")
	fl.StringVar(&f.endpoint, "endpoint", "", "chat endpoint URL (openai: full chat-completions URL)")
	fl.StringVar(&f.model, "model", "", "model id")
	fl.StringVar(&f.apiKeyEnv, "api-key-env", "", "environment variable holding the API key")
	fl.StringVar(&f.shellCommand, "shell-command", "", "shell agent executable (e.g. holmesgpt)")
	fl.StringArrayVar(&f.shellArgs, "shell-arg", nil, "argument for the shell agent (repeatable)")
	fl.StringVar(&f.stubProfile, "stub-profile", "vague",
		"calibration stub answer profile: vague | obvious | shotgun | cited (with --agent=stub)")
	fl.StringVar(&f.stubObvious, "stub-obvious", "",
		"workload the stub names (required for obvious|shotgun|cited): the scenario's ground-truth "+
			"entity, so calibration measures the rubric rather than a wrong guess")
	fl.StringVar(&f.stubCategory, "stub-category", "",
		"category the 'cited' stub claims (required for cited): the scenario's ground-truth category, "+
			"so the profile can show a fully-correct answer still reaches 1.00")
	fl.StringArrayVar(&f.stubShotgun, "stub-shotgun", nil,
		"extra workload the 'shotgun' stub names (repeatable); pass the scenario's decoys to check "+
			"the decoy penalty bites")

	fl.StringVar(&f.mimirURL, "mimir-url", "", "Mimir base URL (enables query_prometheus + list_alerts)")
	fl.StringVar(&f.lokiURL, "loki-url", "", "Loki base URL (enables query_loki)")
	fl.StringVar(&f.tempoURL, "tempo-url", "", "Tempo base URL (enables search_traces)")
	fl.StringVar(&f.tenant, "tenant", "", "X-Scope-OrgID tenant header")

	fl.IntVar(&f.repeats, "repeats", 1, "how many times to run the scenario (variance)")
	fl.IntVar(&f.maxToolCalls, "max-tool-calls", 20, "per-run tool-call budget (0 = uncapped)")
	fl.IntVar(&f.maxTokens, "max-tokens", 100000, "per-run token budget (0 = uncapped)")
	fl.Int64Var(&f.seed, "seed", 0, "seed recorded in the report for reproducibility")
	fl.StringVar(&f.envDigest, "env-digest", "", "identifier of the environment under test, recorded in the report")
	fl.StringVar(&f.condition, "condition", "",
		"label for the telemetry condition the environment is in (e.g. degraded, remediated); recorded in the "+
			"report and used by `bench report --compare`. A label only: you put the environment in that state")

	fl.StringVar(&f.inject, "inject", "auto", "injection mode: auto | script | kubectl | none")
	fl.StringVar(&f.resetScript, "reset-script", "", "script run before injection (script mode)")
	fl.StringVar(&f.cleanupScript, "cleanup-script", "", "script run after each repeat (script mode)")
	fl.StringVar(&f.injectNamespace, "inject-namespace", "", "namespace passed to kubectl (kubectl mode)")
	fl.StringVar(&f.kubeContext, "kube-context", "", "kube context used for injection (kubectl mode)")

	fl.StringVar(&f.judgeEndpoint, "judge-endpoint", "", "LLM-judge chat endpoint (fallback normalizer; disclosed in the report)")
	fl.StringVar(&f.judgeModel, "judge-model", "", "LLM-judge model id")
	fl.StringVar(&f.judgeKeyEnv, "judge-api-key-env", "", "environment variable holding the judge API key")

	fl.BoolVar(&f.localOnly, "local-only", false,
		"guarantee no paid API is touched: require loopback endpoints and refuse API keys")
	fl.DurationVar(&f.judgeTimeout, "judge-timeout", judge.DefaultJudgeTimeout,
		"cap on a single LLM-judge call; judging is one short request, so it gets less than an agent turn")
	fl.DurationVar(&f.agentTimeout, "agent-timeout", agent.DefaultAgentTimeout,
		"cap on a single model call; local inference on CPU needs minutes, not seconds")
	fl.IntVar(&f.minContext, "min-context", local.MinContextTokens,
		"abort if the served context is below this many tokens (silent truncation guard)")

	fl.StringVar(&f.format, "format", "md", "output format: md | json")
	fl.StringVar(&f.out, "out", "", "write the report to this file instead of stdout")

	_ = cmd.MarkFlagRequired("scenario")
	return cmd
}

// enforceLocalPolicy applies the local-inference rules before anything dials
// out. It is opt-in: benchmarking a remote API is a primary use case, so the
// product does not forbid it by default. Pass --local-only when a run must be
// guaranteed never to reach a paid API — the maintainer's own runs do — and
// then it is enforced, not merely intended: endpoints must be loopback and API
// keys are refused outright. Decided 2026-09-30 (DECISIONS.md).
//
// The judge check applies regardless of the local flag, because one model
// grading its own output corrupts a score no matter who is hosting it.
func enforceLocalPolicy(f benchFlags) error {
	if err := local.EnforceDistinctJudge(f.model, f.judgeModel); err != nil {
		return err
	}
	if !f.localOnly {
		return nil
	}
	if err := local.RefuseAPIKey("--api-key-env", f.apiKeyEnv); err != nil {
		return err
	}
	if err := local.RefuseAPIKey("--judge-api-key-env", f.judgeKeyEnv); err != nil {
		return err
	}
	// The shell and stub adapters do not dial an endpoint of ours; shell agents
	// bring their own tooling and are the operator's responsibility.
	if f.agentKind == "openai" || f.agentKind == "anthropic" {
		if err := local.EnforceLoopback("--endpoint", f.endpoint); err != nil {
			return err
		}
	}
	if f.judgeEndpoint != "" {
		if err := local.EnforceLoopback("--judge-endpoint", f.judgeEndpoint); err != nil {
			return err
		}
	}
	return nil
}

// probeModel records what will actually serve the run and refuses to proceed
// under a context too small to hold the tool surface plus telemetry.
//
// It keys off the ENDPOINT, not --local-only. Silent truncation is a property
// of a locally served model (Ollama serves at a small default unless num_ctx is
// set), and the guard against it must not switch off just because the no-paid-
// API policy is off. A loopback OpenAI-compatible endpoint is probed; a remote
// API exposes no management API to probe and is skipped. Returns nil provenance
// where the question does not apply.
func probeModel(ctx context.Context, f benchFlags) (*local.ModelInfo, error) {
	// --min-context=0 disables both the probe and the provenance record. It
	// exists for fakes and for local endpoints that are not Ollama (a proxy such
	// as LiteLLM has no /api/show); a real run should never use it, and a report
	// produced with it carries no model provenance, which is itself the tell.
	if f.agentKind != "openai" || f.minContext <= 0 || !local.IsLoopback(f.endpoint) {
		return nil, nil
	}
	info, err := local.Probe(ctx, f.endpoint, f.model, nil)
	if err != nil {
		return nil, err
	}
	if err := info.RequireContext(f.minContext); err != nil {
		return nil, err
	}
	return &info, nil
}

// enforceDistinctJudgeWeights closes the gap a tag comparison leaves: the judge
// may be a different tag for the agent's own weights (an `ollama create`
// variant, or the base it was built from). It needs both models' provenance, so
// it only applies when both are served locally; for a remote API there is no
// modelfile to read and the tag check in enforceLocalPolicy is what remains.
func enforceDistinctJudgeWeights(ctx context.Context, f benchFlags, agentModel *local.ModelInfo) error {
	if agentModel == nil || f.judgeModel == "" || !local.IsLoopback(f.judgeEndpoint) {
		return nil
	}
	judgeModel, err := local.Probe(ctx, f.judgeEndpoint, f.judgeModel, nil)
	if err != nil {
		return fmt.Errorf("probing judge model: %w", err)
	}
	return local.EnforceDistinctWeights(*agentModel, judgeModel)
}

func buildAgent(f benchFlags) (agent.Agent, error) {
	key := ""
	if f.apiKeyEnv != "" {
		key = os.Getenv(f.apiKeyEnv)
	}
	switch f.agentKind {
	case "openai":
		if f.endpoint == "" || f.model == "" {
			return nil, fmt.Errorf("--agent=openai needs --endpoint and --model")
		}
		return agent.NewOpenAI(agent.OpenAIConfig{
			Endpoint: f.endpoint, Model: f.model, APIKey: key, Timeout: f.agentTimeout,
		}), nil
	case "anthropic":
		if f.model == "" {
			return nil, fmt.Errorf("--agent=anthropic needs --model")
		}
		return agent.NewAnthropic(agent.AnthropicConfig{
			Endpoint: f.endpoint, Model: f.model, APIKey: key, Timeout: f.agentTimeout,
		}), nil
	case "shell":
		if f.shellCommand == "" {
			return nil, fmt.Errorf("--agent=shell needs --shell-command")
		}
		return agent.NewShell(agent.ShellConfig{Command: f.shellCommand, Args: f.shellArgs}), nil
	case "stub":
		// Calibration instrument, not a bench subject: it answers without a model
		// so a scenario's rubric can be probed for discrimination.
		return agent.NewStub(agent.StubConfig{
			Profile:   agent.StubProfile(f.stubProfile),
			Namespace: f.injectNamespace,
			Obvious:   f.stubObvious,
			Category:  f.stubCategory,
			Extra:     f.stubShotgun,
		})
	default:
		return nil, fmt.Errorf("unknown --agent %q (want openai, anthropic, shell or stub)", f.agentKind)
	}
}

// buildTools assembles the read-only MCP surface. A shell agent brings its own
// tooling and the calibration stub reads nothing at all, so an empty surface is
// allowed for those two and only those two.
func buildTools(f benchFlags) (agent.Tools, error) {
	if f.agentKind == "stub" {
		return nil, nil
	}
	var b mcp.Backends
	if f.mimirURL != "" {
		m := backend.NewMimir(f.mimirURL, f.tenant)
		b.Metrics = m
		b.Alerts = m
	}
	if f.lokiURL != "" {
		b.Logs = backend.NewLoki(f.lokiURL, f.tenant)
	}
	if f.tempoURL != "" {
		b.Traces = backend.NewTempo(f.tempoURL, f.tenant)
	}
	reg, err := mcp.NewServer(b)
	if err != nil {
		if f.agentKind == "shell" {
			return nil, nil // shell agents use their own tool access
		}
		return nil, fmt.Errorf("%w (an API agent needs at least --mimir-url)", err)
	}
	return reg, nil
}

// buildProbe returns the steady-state gate. A scenario that declares a
// steadyState block gets a real telemetry probe; one that does not keeps the
// always-ready behavior, and its report must not claim steady state was
// verified.
func buildProbe(f benchFlags, sc bench.Scenario) orchestrator.SteadyStateProbe {
	// Without a metrics backend the calibration stub skips the gate: it reads no
	// telemetry, and calibrating a rubric must not require a live cluster. Given
	// --mimir-url it takes the full lifecycle a real agent does (baseline,
	// inject, steady state, reset), which makes it a free way to validate a
	// scenario's mechanics end to end before spending model time on it.
	if sc.Spec.SteadyState == nil || (f.agentKind == "stub" && f.mimirURL == "") {
		return orchestrator.AlwaysReadyProbe{}
	}
	var q orchestrator.InstantQuerier
	if f.mimirURL != "" {
		q = backend.NewMimir(f.mimirURL, f.tenant)
	}
	// A declared steadyState with no backend is an error the probe raises on the
	// first poll, naming the missing flag, rather than a silent fall-through to
	// always-ready.
	return &orchestrator.PromQLProbe{Q: q}
}

func buildInjector(f benchFlags, sc bench.Scenario) (orchestrator.Injector, error) {
	// The legacy single-adapter modes cannot run a scenario's own reset/cleanup
	// hooks. Refuse rather than skip them: a skipped cleanup leaks a mutated
	// workload into every later repeat and scenario, and a skipped reset leaves
	// the baseline dirty.
	if (f.inject == "script" || f.inject == "kubectl") && (len(sc.Spec.Reset) > 0 || len(sc.Spec.Cleanup) > 0) {
		return nil, fmt.Errorf("scenario %q declares reset/cleanup hooks, which --inject=%s cannot run; "+
			"use --inject=auto (the default)", sc.Metadata.Name, f.inject)
	}
	dir := filepath.Dir(f.scenario)
	switch f.inject {
	case "auto":
		return orchestrator.AutoInjector{
			Manifests: kube.New(dir, f.injectNamespace, f.kubeContext),
			Scripts: orchestrator.ScriptInjector{
				Dir: dir,
				// bash explicitly, not the script's shebang: the maintainer runs
				// argus on Windows, where a .sh file cannot be exec'd directly.
				Shell:   "bash",
				Timeout: 5 * time.Minute,
				Env: []string{
					"ARGUS_NAMESPACE=" + f.injectNamespace,
					"ARGUS_KUBE_CONTEXT=" + f.kubeContext,
				},
			},
		}, nil
	case "none":
		return orchestrator.NoopInjector{}, nil
	case "script":
		return orchestrator.ScriptInjector{
			Dir:           filepath.Dir(f.scenario),
			ResetScript:   f.resetScript,
			CleanupScript: f.cleanupScript,
			Timeout:       5 * time.Minute,
		}, nil
	case "kubectl":
		return kube.New(filepath.Dir(f.scenario), f.injectNamespace, f.kubeContext), nil
	default:
		return nil, fmt.Errorf("unknown --inject %q (want auto, script, kubectl or none)", f.inject)
	}
}

// buildNormalizers always puts the deterministic normalizer first; the LLM
// judge is appended only when configured, so a report reports "llm-judge" only
// when one was actually needed.
func buildNormalizers(f benchFlags) []bench.Normalizer {
	ns := []bench.Normalizer{bench.JSONNormalizer{}}
	if f.judgeEndpoint != "" && f.judgeModel != "" {
		key := ""
		if f.judgeKeyEnv != "" {
			key = os.Getenv(f.judgeKeyEnv)
		}
		ns = append(ns, judge.New(judge.Config{
			Endpoint: f.judgeEndpoint, Model: f.judgeModel, APIKey: key, Timeout: f.judgeTimeout,
		}))
	}
	return ns
}

func writeBenchReport(cmd *cobra.Command, f benchFlags, rep orchestrator.Report) error {
	var payload []byte
	switch f.format {
	case "json":
		b, err := orchestrator.RenderReportJSON(rep)
		if err != nil {
			return err
		}
		payload = b
	case "md":
		payload = []byte(orchestrator.RenderReportMarkdown(rep))
	default:
		return fmt.Errorf("unknown --format %q (want md or json)", f.format)
	}

	if f.out != "" {
		if err := os.WriteFile(f.out, payload, 0o600); err != nil {
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "bench report written: %s\n", f.out)
		return err
	}
	_, err := cmd.OutOrStdout().Write(payload)
	return err
}
