package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Wiring smoke: spec in → blocks + registry out, summary line printed.
func TestSynthHistoryCommand(t *testing.T) {
	out := t.TempDir()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"devtools", "synth-history",
		"--spec", "testdata/synth-spec.yaml", "--out", out})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "synthetic history written") {
		t.Errorf("output = %q", buf.String())
	}
	if _, err := os.Stat(filepath.Join(out, "incidents.yaml")); err != nil {
		t.Error("no incidents.yaml emitted")
	}
	entries, err := os.ReadDir(filepath.Join(out, "blocks"))
	if err != nil || len(entries) == 0 {
		t.Errorf("no blocks emitted: %v", err)
	}
}
