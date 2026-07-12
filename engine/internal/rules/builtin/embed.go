// Package builtin embeds the repository's built-in rule set so `go install`
// binaries work without a rules directory. The source of truth stays at the
// repo root /rules; this tree is a generated copy — run `go generate ./...`
// (or `make rules-sync`) after editing rules, CI fails on drift.
package builtin

//go:generate go run ../../../tools/syncrules

import (
	"embed"

	"github.com/tamen25/Argus/engine/internal/rules"
)

//go:embed spec/*.yaml argus/*.yaml
var rulesFS embed.FS

// Load parses the embedded built-in rules.
func Load() ([]*rules.Rule, error) {
	return rules.LoadFS(rulesFS)
}
