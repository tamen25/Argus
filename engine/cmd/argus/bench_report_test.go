package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runBench runs `argus bench run` against the fake chat server and writes a
// JSON run report for the given condition into dir.
func runBench(t *testing.T, dir, condition, chatURL, mimirURL string) {
	t.Helper()
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{
		"bench", "run",
		"--scenario", writeScenario(t),
		"--endpoint", chatURL, "--model", "m",
		"--mimir-url", mimirURL,
		"--inject", "none",
		"--condition", condition,
		"--format", "json", "--out", filepath.Join(dir, condition+".json"),
		"--min-context", "0", // fake chat server, no /api/show
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("bench run --condition %s: %v", condition, err)
	}
}

func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// The whole path: what `bench run` writes is what `bench report` reads. The
// report loader is strict, so this also catches a run-report field it does not
// know about.
func TestBenchReport_ComparesWhatBenchRunWrote(t *testing.T) {
	chat := chatWithSubmit(t)
	defer chat.Close()
	mimir := mimirStub(t)
	defer mimir.Close()

	dir := t.TempDir()
	runBench(t, dir, "degraded", chat.URL, mimir.URL)
	runBench(t, dir, "remediated", chat.URL, mimir.URL)

	md, err := execute(t, "bench", "report", "--compare", "degraded,remediated", dir)
	if err != nil {
		t.Fatalf("bench report: %v", err)
	}
	for _, want := range []string{
		"# Argus Bench — `degraded` vs `remediated`",
		"| `m` | 1 of 1 |",
		"`cardinality-explosion-checkout`",
		"No significance test is applied",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("comparison lacks %q:\n%s", want, md)
		}
	}

	// Without --compare the same reports give a leaderboard, one board per
	// condition.
	lb, err := execute(t, "bench", "report", dir)
	if err != nil {
		t.Fatalf("bench report (leaderboard): %v", err)
	}
	for _, want := range []string{"## Telemetry condition: `degraded`", "## Telemetry condition: `remediated`"} {
		if !strings.Contains(lb, want) {
			t.Errorf("leaderboard lacks %q:\n%s", want, lb)
		}
	}

	// JSON to a file.
	outPath := filepath.Join(t.TempDir(), "compare.json")
	if _, err := execute(t, "bench", "report", "--compare", "degraded,remediated", "--format", "json", "--out", outPath, dir); err != nil {
		t.Fatal(err)
	}
	js, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `"baseline": "degraded"`) || !strings.Contains(string(js), `"treatment": "remediated"`) {
		t.Errorf("json comparison = %s", js)
	}
}

func TestBenchRun_RecordsTheCondition(t *testing.T) {
	chat := chatWithSubmit(t)
	defer chat.Close()
	mimir := mimirStub(t)
	defer mimir.Close()

	dir := t.TempDir()
	runBench(t, dir, "degraded", chat.URL, mimir.URL)
	b, err := os.ReadFile(filepath.Join(dir, "degraded.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"condition": "degraded"`) {
		t.Errorf("run report does not record its condition:\n%s", b)
	}

	// A label that would be ambiguous in --compare a,b is refused before anything runs.
	if _, err := execute(t, "bench", "run", "--scenario", writeScenario(t), "--agent", "stub",
		"--inject", "none", "--condition", "degraded,remediated"); err == nil ||
		!strings.Contains(err.Error(), "lowercase letters, digits and dashes") {
		t.Errorf("err = %v, want the condition label rejected", err)
	}
}

func TestBenchReport_RejectsWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	notAReport := filepath.Join(dir, "other.json")
	if err := os.WriteFile(notAReport, []byte(`{"baseline":"degraded","agents":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()

	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"no arguments", []string{"bench", "report"}, "requires at least 1 arg"},
		{"missing file", []string{"bench", "report", filepath.Join(dir, "nope.json")}, "nope.json"},
		{"not a run report", []string{"bench", "report", notAReport}, "not a bench run report"},
		{"empty directory", []string{"bench", "report", empty}, "no *.json run reports"},
		{"one condition", []string{"bench", "report", "--compare", "degraded", notAReport}, "baseline,treatment"},
		{"bad label", []string{"bench", "report", "--compare", "Degraded,remediated", notAReport}, "lowercase letters"},
		{"bad format", []string{"bench", "report", "--format", "xml", notAReport}, "unknown --format"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := execute(t, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}
