// Command syncrules copies the repo-root rule YAML (source of truth) into
// engine/internal/rules/builtin for go:embed. Run via `go generate ./...`
// from engine/, or `make rules-sync`. CI fails when the trees drift.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	// Locate the engine module root by walking up to go.mod.
	dir, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	for !exists(filepath.Join(dir, "go.mod")) {
		parent := filepath.Dir(dir)
		if parent == dir {
			fatal(fmt.Errorf("go.mod not found above %s", dir))
		}
		dir = parent
	}
	repo := filepath.Dir(dir)

	for _, sub := range []string{"spec", "argus"} {
		src := filepath.Join(repo, "rules", sub)
		dst := filepath.Join(dir, "internal", "rules", "builtin", sub)
		if err := os.RemoveAll(dst); err != nil {
			fatal(err)
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			fatal(err)
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			fatal(err)
		}
		n := 0
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(src, e.Name()))
			if err != nil {
				fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
				fatal(err)
			}
			n++
		}
		fmt.Printf("synced %d rules into builtin/%s\n", n, sub)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "syncrules:", err)
	os.Exit(1)
}
