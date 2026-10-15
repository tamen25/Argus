package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/atomicfile"
	"github.com/tamen25/Argus/engine/internal/remediate"
	"github.com/tamen25/Argus/engine/internal/remediate/llm"
	"github.com/tamen25/Argus/engine/internal/report"
	"github.com/tamen25/Argus/engine/internal/rules"
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
			return writeOutput(cmd, "", []byte(summary))
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

// runRemediate renders the patch files for every finding of one rule in a
// score report (optionally for one service), writes them to the output
// directory, and returns a summary for the terminal.
func runRemediate(ctx context.Context, opts *remediateOptions) (string, error) {
	explainer, err := newExplainer(opts)
	if err != nil {
		return "", err
	}
	rep, err := readScoreReport(opts.reportPath)
	if err != nil {
		return "", err
	}
	rs, err := loadRules(opts.rulesDir)
	if err != nil {
		return "", err
	}
	tmpl := remediationTemplate(rs, opts.findingID)
	if tmpl == "" {
		return "", fmt.Errorf("rule %q has no remediation template (or is unknown)", opts.findingID)
	}
	found := findingsFor(rep, opts.findingID, opts.service)
	if len(found) == 0 {
		scope := ""
		if opts.service != "" {
			scope = " on service " + opts.service
		}
		return "", fmt.Errorf("no findings for %s%s in %s", opts.findingID, scope, opts.reportPath)
	}

	if err := os.MkdirAll(opts.outDir, 0o750); err != nil {
		return "", err
	}
	written, explained := 0, 0
	for _, f := range found {
		outs, err := remediate.Render(tmpl, remediate.Context{Service: f.Service, Finding: f})
		if err != nil {
			return "", err
		}
		if err := writePatches(opts.outDir, tmpl, f.Service, outs); err != nil {
			return "", err
		}
		written += len(outs)
		if explainer != nil {
			if err := writeExplanation(ctx, explainer, opts.outDir, tmpl, f, outs); err != nil {
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

// newExplainer returns the LLM client --explain asks for, or nil without it.
func newExplainer(opts *remediateOptions) (llm.Client, error) {
	if !opts.explain {
		return nil, nil
	}
	if opts.llmEndpoint == "" || opts.llmModel == "" {
		return nil, fmt.Errorf("--explain requires --llm-endpoint and --llm-model")
	}
	return llm.NewOpenAI(llm.OpenAIConfig{
		Endpoint:  opts.llmEndpoint,
		Model:     opts.llmModel,
		APIKey:    os.Getenv(opts.llmKeyEnv),
		MaxTokens: opts.llmMaxTokens,
		NoRedact:  opts.llmNoRedact,
	}), nil
}

// findingsFor returns the report's findings for one rule, for every service
// or only the named one.
func findingsFor(rep *report.Report, ruleID, service string) []rules.Finding {
	var found []rules.Finding
	for _, s := range rep.Snapshot.Services {
		if service != "" && s.ServiceName != service {
			continue
		}
		for _, f := range s.Findings {
			if f.RuleID == ruleID {
				found = append(found, f)
			}
		}
	}
	return found
}

// writePatches writes a service's rendered patch, one file per format.
func writePatches(outDir, tmpl, service string, outs map[string]string) error {
	for format, content := range outs {
		name := fmt.Sprintf("%s-%s.%s", fileSafe(service), tmpl, format)
		if err := atomicfile.Write(filepath.Join(outDir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// writeExplanation asks the LLM to explain a finding's patch and writes the
// answer next to it, marked as generated: it never changes the patch.
func writeExplanation(ctx context.Context, explainer llm.Client, outDir, tmpl string, f rules.Finding, outs map[string]string) error {
	in, err := explainInputFor(f, tmpl, outs)
	if err != nil {
		return err
	}
	text, err := explainer.Explain(ctx, in)
	if err != nil {
		return fmt.Errorf("llm explain for %s/%s: %w", f.RuleID, f.Service, err)
	}
	note := text + "\n\n---\nGenerated explanation — review; it never changes the patch or the score.\n"
	name := fmt.Sprintf("%s-%s.explanation.md", fileSafe(f.Service), tmpl)
	return atomicfile.Write(filepath.Join(outDir, name), []byte(note), 0o644)
}

// unsafeFileChars is everything but letters, digits, dot, dash and underscore.
var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// fileSafe turns a service name into a file-name component. Service names
// come from telemetry (the service.name attribute an application sends), so
// one named "../../x" must not choose where a patch is written: anything
// outside [A-Za-z0-9._-] becomes "_", and a name of only dots becomes "_".
//
// When that changes the name, a short hash of the original is appended:
// "a/b" and "a_b" would otherwise share one file, and one service's patch
// would overwrite the other's.
func fileSafe(service string) string {
	s := unsafeFileChars.ReplaceAllString(service, "_")
	if strings.Trim(s, ".") == "" {
		s = "_"
	}
	if s == service {
		return s
	}
	sum := sha256.Sum256([]byte(service))
	return s + "-" + hex.EncodeToString(sum[:4])
}

// explainInputFor maps a finding and its rendered patches into the LLM input.
// The patch is the deterministic remediation the LLM explains (alloy.river
// preferred, else the first format by name for a stable choice). The same
// template is rendered a second time from the finding with its service and
// evidence values replaced: that copy holds no telemetry, and it is the one
// sent when redaction is on.
func explainInputFor(f rules.Finding, tmpl string, outs map[string]string) (llm.ExplainInput, error) {
	redacted, err := remediate.Render(tmpl, remediate.Context{Service: redactedValue, Finding: withoutTelemetry(f)})
	if err != nil {
		return llm.ExplainInput{}, err
	}
	ev := make([]llm.Evidence, 0, len(f.Evidence))
	for _, e := range f.Evidence {
		ev = append(ev, llm.Evidence{Summary: e.Summary, Attrs: e.Attrs})
	}
	return llm.ExplainInput{
		RuleID: f.RuleID, RuleName: f.RuleName, Service: f.Service,
		Impact: string(f.Impact), Description: f.Description,
		Patch: preferredPatch(outs), RedactedPatch: preferredPatch(redacted), Evidence: ev,
	}, nil
}

// preferredPatch picks the format the LLM is shown: alloy.river, else the
// first format by name.
func preferredPatch(outs map[string]string) string {
	if p, ok := outs["alloy.river"]; ok {
		return p
	}
	formats := make([]string, 0, len(outs))
	for k := range outs {
		formats = append(formats, k)
	}
	sort.Strings(formats)
	if len(formats) == 0 {
		return ""
	}
	return outs[formats[0]]
}

// redactedValue stands in for every telemetry value in the redacted patch.
// It is not a valid name, so the templates write their placeholders instead.
const redactedValue = "<redacted>"

// withoutTelemetry is a copy of the finding with the service and every
// evidence value replaced, the input for the patch that is safe to send.
func withoutTelemetry(f rules.Finding) rules.Finding {
	out := f
	out.Service = redactedValue
	out.Evidence = make([]rules.Evidence, len(f.Evidence))
	for i, e := range f.Evidence {
		attrs := make(map[string]any, len(e.Attrs))
		for k := range e.Attrs {
			attrs[k] = redactedValue
		}
		out.Evidence[i] = rules.Evidence{Kind: e.Kind, Attrs: attrs}
	}
	return out
}

func redactState(noRedact bool) string {
	if noRedact {
		return "OFF (--llm-no-redact)"
	}
	return "on"
}
