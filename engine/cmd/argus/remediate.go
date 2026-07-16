package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/remediate"
	"github.com/tamen25/Argus/engine/internal/remediate/llm"
	"github.com/tamen25/Argus/engine/internal/report"
	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/rules/builtin"
)

func newRemediateCmd() *cobra.Command {
	opts := &remediateOptions{}
	cmd := &cobra.Command{
		Use:   "remediate",
		Short: "Render remediation patch files (Alloy River + Collector YAML) for a finding",
		Long: "Reads a JSON score report, selects findings by rule ID (optionally one\n" +
			"service), and renders the rule's remediation template into patch files a\n" +
			"human reviews and applies. Argus never touches user systems (read-only\n" +
			"product). Patches are deterministic; --explain adds an optional LLM prose\n" +
			"explanation (edge-only, redaction-on, never auto-applied).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			summary, err := runRemediate(cmd.Context(), opts)
			if err != nil {
				return err
			}
			cmd.Print(summary)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.reportPath, "report", "", "JSON report file from `argus score --output json`")
	cmd.Flags().StringVar(&opts.findingID, "finding", "", "rule ID of the finding to remediate (e.g. MET-001)")
	cmd.Flags().StringVar(&opts.service, "service", "", "only remediate this service's finding")
	cmd.Flags().StringVar(&opts.rulesDir, "rules", "", "extra rule YAML dir overriding/extending built-ins")
	cmd.Flags().StringVar(&opts.outDir, "out", "remediations", "directory for rendered patch files")
	cmd.Flags().BoolVar(&opts.explain, "explain", false, "write an LLM prose explanation alongside each patch (requires --llm-endpoint)")
	cmd.Flags().StringVar(&opts.llmEndpoint, "llm-endpoint", "", "OpenAI-compatible chat completions URL")
	cmd.Flags().StringVar(&opts.llmModel, "llm-model", "", "LLM model name")
	cmd.Flags().StringVar(&opts.llmKeyEnv, "llm-api-key-env", "OPENAI_API_KEY", "env var holding the LLM API key")
	cmd.Flags().IntVar(&opts.llmMaxTokens, "llm-max-tokens", 512, "max tokens for the explanation")
	cmd.Flags().BoolVar(&opts.llmNoRedact, "llm-no-redact", false, "send attribute values to the LLM (redaction is on by default)")
	_ = cmd.MarkFlagRequired("report")
	_ = cmd.MarkFlagRequired("finding")
	return cmd
}

type remediateOptions struct {
	reportPath, findingID, service, rulesDir, outDir string

	explain      bool
	llmEndpoint  string
	llmModel     string
	llmKeyEnv    string
	llmMaxTokens int
	llmNoRedact  bool
}

func runRemediate(ctx context.Context, opts *remediateOptions) (string, error) {
	var explainer llm.Client
	if opts.explain {
		if opts.llmEndpoint == "" || opts.llmModel == "" {
			return "", fmt.Errorf("--explain requires --llm-endpoint and --llm-model")
		}
		explainer = llm.NewOpenAI(llm.OpenAIConfig{
			Endpoint:  opts.llmEndpoint,
			Model:     opts.llmModel,
			APIKey:    os.Getenv(opts.llmKeyEnv),
			MaxTokens: opts.llmMaxTokens,
			NoRedact:  opts.llmNoRedact,
		})
	}

	data, err := os.ReadFile(opts.reportPath)
	if err != nil {
		return "", err
	}
	var rep report.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return "", fmt.Errorf("%s: %w", opts.reportPath, err)
	}
	if rep.Snapshot == nil {
		return "", fmt.Errorf("%s: not a score report (no snapshot)", opts.reportPath)
	}

	rs, err := builtin.Load()
	if err != nil {
		return "", err
	}
	if opts.rulesDir != "" {
		custom, err := rules.LoadDir(opts.rulesDir)
		if err != nil {
			return "", err
		}
		rs = rules.Merge(rs, custom)
	}
	var tmpl string
	for _, r := range rs {
		if r.ID == opts.findingID {
			tmpl = r.Remediation.Template
		}
	}
	if tmpl == "" {
		return "", fmt.Errorf("rule %q has no remediation template (or is unknown)", opts.findingID)
	}

	var found []rules.Finding
	for _, s := range rep.Snapshot.Services {
		if opts.service != "" && s.ServiceName != opts.service {
			continue
		}
		for _, f := range s.Findings {
			if f.RuleID == opts.findingID {
				found = append(found, f)
			}
		}
	}
	if len(found) == 0 {
		scope := ""
		if opts.service != "" {
			scope = " on service " + opts.service
		}
		return "", fmt.Errorf("no findings for %s%s in %s", opts.findingID, scope, opts.reportPath)
	}

	if err := os.MkdirAll(opts.outDir, 0o755); err != nil {
		return "", err
	}
	written, explained := 0, 0
	for _, f := range found {
		outs, err := remediate.Render(tmpl, remediate.Context{Service: f.Service, Finding: f})
		if err != nil {
			return "", err
		}
		for format, content := range outs {
			name := fmt.Sprintf("%s-%s.%s", f.Service, tmpl, format)
			if err := os.WriteFile(filepath.Join(opts.outDir, name), []byte(content), 0o644); err != nil {
				return "", err
			}
			written++
		}
		if explainer != nil {
			text, err := explainer.Explain(ctx, explainInputFor(f, outs))
			if err != nil {
				return "", fmt.Errorf("llm explain for %s/%s: %w", f.RuleID, f.Service, err)
			}
			note := text + "\n\n---\nGenerated explanation — review; it never changes the patch or the score.\n"
			if err := os.WriteFile(filepath.Join(opts.outDir, fmt.Sprintf("%s-%s.explanation.md", f.Service, tmpl)), []byte(note), 0o644); err != nil {
				return "", err
			}
			explained++
		}
	}

	var b strings.Builder
	noun := "services"
	if len(found) == 1 {
		noun = "service"
	}
	fmt.Fprintf(&b, "Rendered %q for %s: %d %s, %d patch file(s) in %s.\n",
		tmpl, opts.findingID, len(found), noun, written, opts.outDir)
	if explained > 0 {
		fmt.Fprintf(&b, "Wrote %d LLM explanation file(s) (redaction %s).\n", explained, redactState(opts.llmNoRedact))
	}
	fmt.Fprintf(&b, "Review before applying — Argus never modifies your systems.\n")
	return b.String(), nil
}

// explainInputFor maps a finding + its rendered patches into the LLM input.
// The patch is the deterministic remediation the LLM explains (alloy.river
// preferred, else the first format by name for a stable choice).
func explainInputFor(f rules.Finding, outs map[string]string) llm.ExplainInput {
	patch := outs["alloy.river"]
	if patch == "" {
		formats := make([]string, 0, len(outs))
		for k := range outs {
			formats = append(formats, k)
		}
		sort.Strings(formats)
		if len(formats) > 0 {
			patch = outs[formats[0]]
		}
	}
	ev := make([]llm.Evidence, 0, len(f.Evidence))
	for _, e := range f.Evidence {
		ev = append(ev, llm.Evidence{Summary: e.Summary, Attrs: e.Attrs})
	}
	return llm.ExplainInput{
		RuleID: f.RuleID, RuleName: f.RuleName, Service: f.Service,
		Impact: string(f.Impact), Description: f.Description, Patch: patch, Evidence: ev,
	}
}

func redactState(noRedact bool) string {
	if noRedact {
		return "OFF (--llm-no-redact)"
	}
	return "on"
}
