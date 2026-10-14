package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	for _, content := range []string{"first", "second, longer than the first"} {
		if err := Write(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Errorf("file = %q, want %q", got, content)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only the file (no temporary left behind)", len(entries))
	}
}

// A write that cannot complete leaves the existing file untouched and no
// temporary file behind.
func TestWriteFailureLeavesTheOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	if err := Write(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory where the file should be makes the final rename fail.
	blocked := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(filepath.Join(blocked, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(blocked, []byte("new"), 0o600); err == nil {
		t.Fatal("writing over a non-empty directory succeeded")
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Errorf("existing file = %q, want it untouched", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "report.json" && e.Name() != "blocked" {
			t.Errorf("left behind %s", e.Name())
		}
	}
}
