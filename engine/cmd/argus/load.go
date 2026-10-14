package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/tamen25/Argus/engine/internal/report"
	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/rules/builtin"
)

// loadRules returns the built-in rules, merged with a directory of custom ones
// when rulesDir is set (custom rules override built-ins with the same ID).
func loadRules(rulesDir string) ([]*rules.Rule, error) {
	rs, err := builtin.Load()
	if err != nil || rulesDir == "" {
		return rs, err
	}
	custom, err := rules.LoadDir(rulesDir)
	if err != nil {
		return nil, err
	}
	return rules.Merge(rs, custom), nil
}

// remediationTemplate is the template name of the rule with this ID, or "" if
// there is no such rule or it has no template.
func remediationTemplate(rs []*rules.Rule, ruleID string) string {
	for _, r := range rs {
		if r.ID == ruleID {
			return r.Remediation.Template
		}
	}
	return ""
}

// readScoreReport reads a JSON report written by `argus score --output json`.
func readScoreReport(path string) (*report.Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rep report.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if rep.Snapshot == nil {
		return nil, fmt.Errorf("%s: not a score report (no snapshot)", path)
	}
	return &rep, nil
}
