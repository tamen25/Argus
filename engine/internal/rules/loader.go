package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadDir loads every *.yaml/*.yml rule file under dir (recursively),
// skipping the vendored upstream/ markdown tree.
func LoadDir(dirs ...string) ([]*Rule, error) {
	var blobs [][]byte
	var names []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			blobs = append(blobs, b)
			names = append(names, path)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("loading rules from %s: %w", dir, err)
		}
	}
	rs, err := LoadBytes(blobs...)
	if err != nil {
		return nil, fmt.Errorf("loading rules (%s): %w", strings.Join(names, ", "), err)
	}
	return rs, nil
}

// LoadBytes parses one rule per YAML document. The decoder is strict: unknown
// fields are rejected (architecture rule 4: versioned schema, strict loader).
// CEL criteria are compiled here so a bad rule fails at load, not at runtime.
func LoadBytes(blobs ...[]byte) ([]*Rule, error) {
	var out []*Rule
	seen := map[string]bool{}
	for _, b := range blobs {
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		for {
			var r Rule
			err := dec.Decode(&r)
			if err != nil {
				if err.Error() == "EOF" {
					break
				}
				return nil, fmt.Errorf("parsing rule: %w", err)
			}
			if err := r.validate(); err != nil {
				return nil, err
			}
			if seen[r.ID] {
				return nil, fmt.Errorf("duplicate rule id %q", r.ID)
			}
			seen[r.ID] = true
			if _, err := compileCriteria(&r); err != nil {
				return nil, fmt.Errorf("rule %q: compiling criteria: %w", r.ID, err)
			}
			rr := r
			out = append(out, &rr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
