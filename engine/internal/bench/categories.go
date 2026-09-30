package bench

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// CategoriesKind is the kind of a category list file.
const CategoriesKind = "BenchCategories"

// MinCategories is the smallest list an agent may be offered. The fault
// category is scored by exact match, so the agent has to be shown the
// vocabulary — but a list of one or two is the answer, not a vocabulary.
const MinCategories = 5

// Category is one fault classification an agent may choose.
type Category struct {
	Name string `yaml:"name" json:"name"`
	// Description is one line telling the agent what the category means. It
	// describes a kind of fault, never a scenario.
	Description string `yaml:"description" json:"description"`
}

// Categories is the closed list of fault categories for a scenario library. The
// first real-model run answered "PerformanceDegradation" to a scenario whose
// ground truth was "deploy-regression": it had never been shown the list, and
// no agent can match a slug it has not seen. Every agent is now offered the
// same list, and a scenario's own category must be on it.
type Categories struct {
	APIVersion string     `yaml:"apiVersion"`
	Kind       string     `yaml:"kind"`
	Categories []Category `yaml:"categories"`
}

// categoryName is the shape of a category slug. `+` joins ITBench fault ids.
var categoryName = regexp.MustCompile(`^[a-z0-9][a-z0-9+._-]*$`)

// LoadCategories reads and validates a category list. Strict, like the
// scenario loader: an unknown key is an error.
func LoadCategories(path string) (Categories, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Categories{}, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var c Categories
	if err := dec.Decode(&c); err != nil {
		return Categories{}, fmt.Errorf("parsing categories %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return Categories{}, fmt.Errorf("categories %s: %w", path, err)
	}
	return c, nil
}

func (c Categories) validate() error {
	if c.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion %q, want %q", c.APIVersion, APIVersion)
	}
	if c.Kind != CategoriesKind {
		return fmt.Errorf("kind %q, want %q", c.Kind, CategoriesKind)
	}
	if len(c.Categories) < MinCategories {
		return fmt.Errorf("%d categories listed, want at least %d: a shorter list gives the answer away",
			len(c.Categories), MinCategories)
	}
	seen := map[string]bool{}
	for i, cat := range c.Categories {
		if !categoryName.MatchString(cat.Name) {
			return fmt.Errorf("categories[%d]: name %q is not a lowercase slug", i, cat.Name)
		}
		if seen[cat.Name] {
			return fmt.Errorf("categories[%d]: %q is listed twice", i, cat.Name)
		}
		seen[cat.Name] = true
		if strings.TrimSpace(cat.Description) == "" {
			return fmt.Errorf("categories[%d]: %q has no description", i, cat.Name)
		}
	}
	return nil
}

// Sorted returns the categories ordered by name. Agents are always shown this
// order, so position carries no hint about any one scenario.
func (c Categories) Sorted() []Category {
	out := append([]Category{}, c.Categories...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names returns the category names in Sorted order.
func (c Categories) Names() []string {
	sorted := c.Sorted()
	names := make([]string, len(sorted))
	for i, cat := range sorted {
		names[i] = cat.Name
	}
	return names
}

// Has reports whether name is on the list. Case-insensitive, matching how a
// diagnosis is scored.
func (c Categories) Has(name string) bool {
	name = strings.TrimSpace(name)
	for _, cat := range c.Categories {
		if strings.EqualFold(cat.Name, name) {
			return true
		}
	}
	return false
}

// Check reports an error when a scenario's ground-truth category is not on the
// list. Such a scenario could never be answered correctly: its category would
// not be among the choices the agent is given.
func (c Categories) Check(sc Scenario) error {
	if !c.Has(sc.Spec.GroundTruth.Category) {
		return fmt.Errorf("scenario %s: ground-truth category %q is not in the category list (%s)",
			sc.Metadata.Name, sc.Spec.GroundTruth.Category, strings.Join(c.Names(), ", "))
	}
	return nil
}

// AddCategories merges categories into the list file at path, creating it if
// needed, and returns how many the file then lists. An importer uses it to
// build the list for the scenarios it emits. The result may be shorter than
// MinCategories; LoadCategories says so when a run needs the list.
func AddCategories(path string, add []Category) (int, error) {
	list := Categories{APIVersion: APIVersion, Kind: CategoriesKind}
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, &list); err != nil {
			return 0, fmt.Errorf("parsing categories %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}

	seen := map[string]bool{}
	for _, c := range list.Categories {
		seen[c.Name] = true
	}
	for _, c := range add {
		if !seen[c.Name] {
			seen[c.Name] = true
			list.Categories = append(list.Categories, c)
		}
	}
	list.Categories = list.Sorted()

	out, err := yaml.Marshal(list)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return 0, err
	}
	return len(list.Categories), nil
}
