package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// A report goes to standard output, so `argus cost > report.md` works.
// cobra's cmd.Print would send it to stderr unless an output was set, and the
// command tests set one, which hid the bug.
func TestWriteOutputUsesStdout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()

	cmd := &cobra.Command{}
	var stderr bytes.Buffer
	cmd.SetErr(&stderr) // stderr captured, stdout left as the process's
	if err := writeOutput(cmd, "", []byte("the report")); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	got, _ := io.ReadAll(r)
	if string(got) != "the report" || stderr.Len() != 0 {
		t.Errorf("stdout = %q, stderr = %q; want the report on stdout only", got, stderr.String())
	}
}

// No command may print a report with cmd.Print / Println / Printf: they fall
// back to stderr. Status messages use cmd.PrintErr* deliberately.
func TestNoCommandPrintsReportsToStderr(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"cmd.Print(", "cmd.Println(", "cmd.Printf("} {
			if bytes.Contains(src, []byte(bad)) {
				t.Errorf("%s uses %s: write reports with writeOutput", f, bad)
			}
		}
	}
}

func TestParseWindow(t *testing.T) {
	if _, _, err := parseWindow("2026-07-16T12:00:00Z", "2026-07-16T13:00:00Z"); err != nil {
		t.Errorf("a valid window: %v", err)
	}
	for _, c := range [][2]string{
		{"yesterday", "2026-07-16T13:00:00Z"},
		{"2026-07-16T12:00:00Z", "later"},
		{"2026-07-16T13:00:00Z", "2026-07-16T12:00:00Z"},
		{"2026-07-16T12:00:00Z", "2026-07-16T12:00:00Z"},
	} {
		if _, _, err := parseWindow(c[0], c[1]); err == nil {
			t.Errorf("parseWindow(%q, %q) accepted", c[0], c[1])
		}
	}
}
