package cost_test

import (
	"path/filepath"
	"testing"

	"github.com/tamen25/Argus/engine/internal/cost"
)

// The shipped pricing templates must always load and validate — a broken
// template is a broken first-run experience.
func TestShippedTemplatesLoad(t *testing.T) {
	for _, name := range []string{"aws.yaml", "generic.yaml"} {
		path := filepath.Join("..", "..", "..", "pricing", name)
		p, err := cost.LoadPricing(path)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if p.Currency == "" {
			t.Errorf("%s: no currency", name)
		}
	}
}
