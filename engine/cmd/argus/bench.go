package main

import (
	"fmt"
	"os"
	"path/filepath"
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
	cmd.AddCommand(newBenchRunCmd(), newBenchImportITBenchCmd())
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

	mimirURL string
	lokiURL  string
	tempoURL string
	tenant   string

	repeats      int
	maxToolCalls int
	maxTokens    int
	seed         int64
	envDigest    string

	inject          string
	resetScript     string
	cleanupScript   string
	injectNamespace string
	kubeContext     string

	judgeEndpoint string
	judgeModel    string
	judgeKeyEnv   string

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
  --inject=script   run the scenario's script steps locally
  --inject=kubectl  apply the scenario's kubectl/chaosmesh manifests with
                    kubectl, and delete them again on cleanup
  --inject=none     inject nothing; score against an environment you already
                    put into the desired state yourself

Each injector rejects step types it cannot execute rather than skipping them,
so a scenario is never scored against an environment that was never faulted.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sc, err := bench.LoadScenario(f.scenario)
			if err != nil {
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
			inj, err := buildInjector(f)
			if err != nil {
				return err
			}

			opts := orchestrator.Options{
				Repeats:     f.repeats,
				Budget:      agent.Budget{MaxToolCalls: f.maxToolCalls, MaxTokens: f.maxTokens},
				Normalizers: buildNormalizers(f),
				Seed:        f.seed,
				EnvDigest:   f.envDigest,
			}

			rep, err := orchestrator.Run(cmd.Context(), sc, ag, tools, inj, orchestrator.AlwaysReadyProbe{}, opts)
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

	fl.StringVar(&f.mimirURL, "mimir-url", "", "Mimir base URL (enables query_prometheus + list_alerts)")
	fl.StringVar(&f.lokiURL, "loki-url", "", "Loki base URL (enables query_loki)")
	fl.StringVar(&f.tempoURL, "tempo-url", "", "Tempo base URL (enables search_traces)")
	fl.StringVar(&f.tenant, "tenant", "", "X-Scope-OrgID tenant header")

	fl.IntVar(&f.repeats, "repeats", 1, "how many times to run the scenario (variance)")
	fl.IntVar(&f.maxToolCalls, "max-tool-calls", 20, "per-run tool-call budget (0 = uncapped)")
	fl.IntVar(&f.maxTokens, "max-tokens", 100000, "per-run token budget (0 = uncapped)")
	fl.Int64Var(&f.seed, "seed", 0, "seed recorded in the report for reproducibility")
	fl.StringVar(&f.envDigest, "env-digest", "", "identifier of the environment under test, recorded in the report")

	fl.StringVar(&f.inject, "inject", "script", "injection mode: script | kubectl | none")
	fl.StringVar(&f.resetScript, "reset-script", "", "script run before injection (script mode)")
	fl.StringVar(&f.cleanupScript, "cleanup-script", "", "script run after each repeat (script mode)")
	fl.StringVar(&f.injectNamespace, "inject-namespace", "", "namespace passed to kubectl (kubectl mode)")
	fl.StringVar(&f.kubeContext, "kube-context", "", "kube context used for injection (kubectl mode)")

	fl.StringVar(&f.judgeEndpoint, "judge-endpoint", "", "LLM-judge chat endpoint (fallback normalizer; disclosed in the report)")
	fl.StringVar(&f.judgeModel, "judge-model", "", "LLM-judge model id")
	fl.StringVar(&f.judgeKeyEnv, "judge-api-key-env", "", "environment variable holding the judge API key")

	fl.StringVar(&f.format, "format", "md", "output format: md | json")
	fl.StringVar(&f.out, "out", "", "write the report to this file instead of stdout")

	_ = cmd.MarkFlagRequired("scenario")
	return cmd
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
		return agent.NewOpenAI(agent.OpenAIConfig{Endpoint: f.endpoint, Model: f.model, APIKey: key}), nil
	case "anthropic":
		if f.model == "" {
			return nil, fmt.Errorf("--agent=anthropic needs --model")
		}
		return agent.NewAnthropic(agent.AnthropicConfig{Endpoint: f.endpoint, Model: f.model, APIKey: key}), nil
	case "shell":
		if f.shellCommand == "" {
			return nil, fmt.Errorf("--agent=shell needs --shell-command")
		}
		return agent.NewShell(agent.ShellConfig{Command: f.shellCommand, Args: f.shellArgs}), nil
	default:
		return nil, fmt.Errorf("unknown --agent %q (want openai, anthropic or shell)", f.agentKind)
	}
}

// buildTools assembles the read-only MCP surface. A shell agent brings its own
// tooling, so an empty surface is allowed there and only there.
func buildTools(f benchFlags) (agent.Tools, error) {
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

func buildInjector(f benchFlags) (orchestrator.Injector, error) {
	switch f.inject {
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
		return nil, fmt.Errorf("unknown --inject %q (want script, kubectl or none)", f.inject)
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
		ns = append(ns, judge.New(judge.Config{Endpoint: f.judgeEndpoint, Model: f.judgeModel, APIKey: key}))
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
