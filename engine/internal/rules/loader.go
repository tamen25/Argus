package rules

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadDir loads every *.yaml/*.yml rule file under each dir, recursively.
//
// Each dir is opened as an os.Root, so every read stays inside it: a symlink,
// or a file swapped for one while the walk runs, cannot make the loader read
// anything outside the directory it was given.
func LoadDir(dirs ...string) ([]*Rule, error) {
	var blobs [][]byte
	var names []string
	for _, dir := range dirs {
		b, n, err := readDirRules(dir)
		if err != nil {
			return nil, fmt.Errorf("loading rules from %s: %w", dir, err)
		}
		blobs = append(blobs, b...)
		names = append(names, n...)
	}
	rs, err := LoadBytes(blobs...)
	if err != nil {
		return nil, fmt.Errorf("loading rules (%s): %w", strings.Join(names, ", "), err)
	}
	return rs, nil
}

func readDirRules(dir string) (blobs [][]byte, names []string, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }()
	blobs, paths, err := readRuleFiles(root.FS())
	for _, p := range paths {
		names = append(names, filepath.Join(dir, p))
	}
	return blobs, names, err
}

// LoadFS loads every rule file in an fs.FS (the embedded built-in rules).
func LoadFS(fsys fs.FS) ([]*Rule, error) {
	blobs, _, err := readRuleFiles(fsys)
	if err != nil {
		return nil, err
	}
	return LoadBytes(blobs...)
}

// readRuleFiles reads every *.yaml/*.yml file in fsys, in walk (lexical)
// order, and returns their contents and paths.
func readRuleFiles(fsys fs.FS) (blobs [][]byte, paths []string, err error) {
	err = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := strings.ToLower(filepath.Ext(path)); ext != ".yaml" && ext != ".yml" {
			return nil
		}
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		blobs = append(blobs, b)
		paths = append(paths, path)
		return nil
	})
	return blobs, paths, err
}

// Merge overlays rules onto a base set: an override with the same ID replaces
// the base rule (CLI semantics: built-ins first, --rules dir on top).
func Merge(base, overrides []*Rule) []*Rule {
	byID := make(map[string]*Rule, len(base)+len(overrides))
	for _, r := range base {
		byID[r.ID] = r
	}
	for _, r := range overrides {
		byID[r.ID] = r
	}
	out := make([]*Rule, 0, len(byID))
	for _, r := range byID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// LoadBytes parses one rule per YAML document. The decoder is strict: unknown
// fields are rejected (architecture rule 4: versioned schema, strict loader).
// CEL criteria are compiled here so a bad rule fails at load, not at runtime.
func LoadBytes(blobs ...[]byte) ([]*Rule, error) {
	var out []*Rule
	seen := map[string]bool{}
	for _, b := range blobs {
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		for {
			var r Rule
			err := dec.Decode(&r)
			if errors.Is(err, io.EOF) {
				break // no more documents in this blob
			}
			if err != nil {
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
			out = append(out, &r) // r is declared inside the loop: a new variable each document
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
