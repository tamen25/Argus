package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/remediate"
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
			"product); deterministic output — LLM explanations are Phase 2.",
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
	_ = cmd.MarkFlagRequired("report")
	_ = cmd.MarkFlagRequired("finding")
	return cmd
}

type remediateOptions struct {
	reportPath, findingID, service, rulesDir, outDir string
}

func runRemediate(_ context.Context, opts *remediateOptions) (string, error) {
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
	written := 0
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
	}

	var b strings.Builder
	noun := "services"
	if len(found) == 1 {
		noun = "service"
	}
	fmt.Fprintf(&b, "Rendered %q for %s: %d %s, %d patch file(s) in %s.\n",
		tmpl, opts.findingID, len(found), noun, written, opts.outDir)
	fmt.Fprintf(&b, "Review before applying — Argus never modifies your systems.\n")
	return b.String(), nil
}
