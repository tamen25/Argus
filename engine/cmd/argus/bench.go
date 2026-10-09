package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/agent"
	"github.com/tamen25/Argus/engine/internal/bench/inject/kube"
	"github.com/tamen25/Argus/engine/internal/bench/judge"
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
	categories   string

	inject          string
	resetScript     string
	cleanupScript   string
	injectNamespace string
	kubeContext     string
	topology        string

	judgeEndpoint string
	judgeModel    string
	judgeKeyEnv   string

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

The judge model must differ from the agent model: a model that misreads its
own output the same way twice would launder that error into the score.`,
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
			categories, err := loadCategories(f, sc)
			if err != nil {
				return err
			}
			if err := enforceDistinctJudge(f.model, f.judgeModel); err != nil {
				return err
			}
			ag, err := buildAgent(f)
			if err != nil {
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
				Repeats:      f.repeats,
				Budget:       agent.Budget{MaxToolCalls: f.maxToolCalls, MaxTokens: f.maxTokens},
				Normalizers:  buildNormalizers(f),
				Seed:         f.seed,
				EnvDigest:    f.envDigest,
				Condition:    f.condition,
				Categories:   categories,
				Model:        modelInfo(f),
				AgentTimeout: f.agentTimeout,
			}

			// An interrupt (Ctrl-C, or a matrix being stopped) cancels the run,
			// and the orchestrator still cleans up the fault it injected. Without
			// this the process died with the fault in place.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			rep, err := orchestrator.Run(ctx, sc, ag, tools, inj, buildProbe(f, sc), opts)
			if err != nil {
				// No report on interruption: a partial one would look like a
				// finished cell to a matrix that resumes by skipping reports.
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
	fl.StringVar(&f.categories, "categories", "",
		"fault category list offered to the agent (default: categories.yaml next to the scenario)")
	fl.StringVar(&f.condition, "condition", "",
		"label for the telemetry condition the environment is in (e.g. degraded, remediated); recorded in the "+
			"report and used by `bench report --compare`. A label only: you put the environment in that state")

	fl.StringVar(&f.inject, "inject", "auto", "injection mode: auto | script | kubectl | none")
	fl.StringVar(&f.resetScript, "reset-script", "", "script run before injection (script mode)")
	fl.StringVar(&f.cleanupScript, "cleanup-script", "", "script run after each repeat (script mode)")
	fl.StringVar(&f.injectNamespace, "inject-namespace", "", "namespace passed to kubectl (kubectl mode)")
	fl.StringVar(&f.kubeContext, "kube-context", "", "kube context used for injection and for get_k8s_topology")
	fl.StringVar(&f.topology, "topology", "auto",
		"get_k8s_topology backend: auto (kubectl if on PATH) | kubectl | none. Lists workload identities only, never status")

	fl.StringVar(&f.judgeEndpoint, "judge-endpoint", "", "LLM-judge chat endpoint (fallback normalizer; disclosed in the report)")
	fl.StringVar(&f.judgeModel, "judge-model", "", "LLM-judge model id")
	fl.StringVar(&f.judgeKeyEnv, "judge-api-key-env", "", "environment variable holding the judge API key")

	fl.DurationVar(&f.judgeTimeout, "judge-timeout", judge.DefaultJudgeTimeout,
		"cap on a single LLM-judge call; judging is one short request, so it gets less than an agent turn")
	fl.DurationVar(&f.agentTimeout, "agent-timeout", agent.DefaultAgentTimeout,
		"cap on a single model call; a reasoning model can spend minutes on one turn")

	fl.StringVar(&f.format, "format", "md", "output format: md | json")
	fl.StringVar(&f.out, "out", "", "write the report to this file instead of stdout")

	_ = cmd.MarkFlagRequired("scenario")
	return cmd
}

// ErrJudgeSameModel is returned when the LLM judge would grade its own model.
var ErrJudgeSameModel = errors.New("the judge model must differ from the agent model")

// enforceDistinctJudge refuses a judge that is the agent's own model: one model
// grading its own output corrupts a score, because it misreads its own answer
// the same way twice.
func enforceDistinctJudge(agentModel, judgeModel string) error {
	if strings.TrimSpace(judgeModel) == "" {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(agentModel), strings.TrimSpace(judgeModel)) {
		return fmt.Errorf("%w: both are %q", ErrJudgeSameModel, agentModel)
	}
	return nil
}

// modelInfo records which model served the run, for the report. Adapters
// without a model id (stub, shell) record none.
func modelInfo(f benchFlags) *orchestrator.ModelInfo {
	if f.model == "" || (f.agentKind != "openai" && f.agentKind != "anthropic") {
		return nil
	}
	return &orchestrator.ModelInfo{Endpoint: f.endpoint, Model: f.model}
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
	var mimir *backend.Mimir
	if f.mimirURL != "" {
		mimir = backend.NewMimir(f.mimirURL, f.tenant)
		b.Metrics = mimir
		b.Alerts = mimir
		b.MetricsCatalog = mimir
	}
	if f.lokiURL != "" {
		l := backend.NewLoki(f.lokiURL, f.tenant)
		b.Logs = l
		b.LogsCatalog = l
	}
	if f.tempoURL != "" {
		t := backend.NewTempo(f.tempoURL, f.tenant)
		b.Traces = t
		b.TracesCatalog = t
	}
	useKubectl, err := topologyMode(f.topology)
	if err != nil {
		return nil, err
	}
	if useKubectl {
		// The bench's own fault objects carry the sweep label; hide them. They
		// are the apparatus, and a Deployment named argus-fault-cardinality
		// would hand the agent the answer.
		exclude := strings.Replace(kube.ManagedBy, "=", "!=", 1)
		b.Topology = backend.NewKubeTopology(f.kubeContext, exclude, mimir)
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

// topologyMode resolves --topology. "auto" offers get_k8s_topology when kubectl
// is on PATH, so the surface stays honest: a tool is only offered if it can
// answer.
func topologyMode(mode string) (bool, error) {
	switch mode {
	case "auto":
		_, err := exec.LookPath("kubectl")
		return err == nil, nil
	case "kubectl":
		if _, err := exec.LookPath("kubectl"); err != nil {
			return false, fmt.Errorf("--topology=kubectl: kubectl is not on PATH")
		}
		return true, nil
	case "none":
		return false, nil
	default:
		return false, fmt.Errorf("unknown --topology %q (want auto, kubectl or none)", mode)
	}
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

// loadCategories loads the category list the agent will choose from: the file
// given by --categories, or categories.yaml beside the scenario. There is no
// "none": the category is scored by exact match, so a run without the list
// would score every agent on whether it guessed the library's slugs.
func loadCategories(f benchFlags, sc bench.Scenario) (bench.Categories, error) {
	path := f.categories
	if path == "" {
		path = filepath.Join(filepath.Dir(f.scenario), "categories.yaml")
	}
	c, err := bench.LoadCategories(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return bench.Categories{}, fmt.Errorf(
				"no fault category list at %s: the agent must be shown the categories it is scored against "+
					"(create the file, or pass --categories; see docs/bench/authoring-scenarios.md)", path)
		}
		return bench.Categories{}, err
	}
	if err := c.Check(sc); err != nil {
		return bench.Categories{}, err
	}
	return c, nil
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
