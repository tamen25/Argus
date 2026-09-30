package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/bench/leaderboard"
	"github.com/tamen25/Argus/engine/internal/bench/orchestrator"
)

// conditionLabel is what a telemetry-condition label may look like. It is a
// name the operator picks; keeping it to a slug keeps it safe in file names and
// unambiguous in `--compare a,b`.
var conditionLabel = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func validCondition(label string) error {
	if !conditionLabel.MatchString(label) {
		return fmt.Errorf("condition %q: use lowercase letters, digits and dashes (e.g. degraded, remediated)", label)
	}
	return nil
}

func newBenchReportCmd() *cobra.Command {
	var compare, format, out string

	cmd := &cobra.Command{
		Use:   "report <run-report.json | directory>...",
		Short: "Build a leaderboard, or compare two telemetry conditions, from bench run reports",
		Long: `Reads the JSON reports written by 'argus bench run --format json' and builds
one of two tables.

Without --compare: a leaderboard — agents × scenarios, one table per telemetry
condition found in the reports.

With --compare baseline,treatment: the comparison of two telemetry conditions
(the labels given to 'bench run --condition'), per agent and per scenario:

    argus bench report --compare degraded,remediated runs/

Δ is treatment − baseline over the scenarios an agent answered under both
conditions. A scenario missing or unanswered on either side is listed as
excluded; it is never counted as zero.

Runs of the same agent on the same scenario under the same condition are pooled
across reports, and the means recomputed from the runs. A directory argument
reads every *.json file in it (not recursively). Everything in the output can be
recomputed by hand from the run reports: no model is involved.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reports, err := loadRunReports(args)
			if err != nil {
				return err
			}

			var md string
			var data any
			if compare == "" {
				lb, err := leaderboard.Build(reports)
				if err != nil {
					return err
				}
				md, data = leaderboard.RenderLeaderboardMarkdown(lb), lb
			} else {
				baseline, treatment, ok := strings.Cut(compare, ",")
				if !ok {
					return fmt.Errorf("--compare wants two conditions: baseline,treatment (got %q)", compare)
				}
				baseline, treatment = strings.TrimSpace(baseline), strings.TrimSpace(treatment)
				for _, label := range []string{baseline, treatment} {
					if err := validCondition(label); err != nil {
						return err
					}
				}
				c, err := leaderboard.Compare(reports, baseline, treatment)
				if err != nil {
					return err
				}
				md, data = leaderboard.RenderComparisonMarkdown(c), c
			}

			var payload []byte
			switch format {
			case "md":
				payload = []byte(md)
			case "json":
				if payload, err = leaderboard.RenderJSON(data); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown --format %q (want md or json)", format)
			}

			if out != "" {
				if err := os.WriteFile(out, payload, 0o600); err != nil {
					return err
				}
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "bench report written: %s\n", out)
				return err
			}
			_, err = cmd.OutOrStdout().Write(payload)
			return err
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&compare, "compare", "", "compare two telemetry conditions: baseline,treatment (e.g. degraded,remediated)")
	fl.StringVar(&format, "format", "md", "output format: md | json")
	fl.StringVar(&out, "out", "", "write the report to this file instead of stdout")
	return cmd
}

// loadRunReports reads run reports from files and directories. It is strict:
// a file that is not a run report is an error rather than a skipped row, since
// a silently dropped report changes every mean built from the rest.
func loadRunReports(args []string) ([]orchestrator.Report, error) {
	var paths []string
	for _, a := range args {
		info, err := os.Stat(a)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			paths = append(paths, a)
			continue
		}
		matches, err := filepath.Glob(filepath.Join(a, "*.json"))
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("%s: no *.json run reports in this directory", a)
		}
		sort.Strings(matches)
		paths = append(paths, matches...)
	}

	reports := make([]orchestrator.Report, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var r orchestrator.Report
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("%s: not a bench run report: %w", p, err)
		}
		if r.Scenario == "" || r.Agent == "" {
			return nil, fmt.Errorf("%s: not a bench run report (no scenario or agent)", p)
		}
		reports = append(reports, r)
	}
	return reports, nil
}
