// Package remediate renders deterministic configuration patches for
// findings: Alloy (River) and OTel Collector YAML a human reviews and
// applies. Read-only product (architecture rule 5) — Argus generates files,
// never touches user infrastructure. LLM-drafted explanation text is a
// Phase 2 concern and lives elsewhere; nothing here may import it.
package remediate

import (
	"embed"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/tamen25/Argus/engine/internal/rules"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// Context is everything a template may substitute.
type Context struct {
	Service string
	Finding rules.Finding
}

// formats maps output name -> template file suffix.
var formats = map[string]string{
	"alloy.river":    "river",
	"collector.yaml": "yaml",
}

// Render produces every output format for one template. Output is
// deterministic: same context, same bytes.
func Render(name string, ctx Context) (map[string]string, error) {
	if !Available()[name] {
		names := make([]string, 0, len(Available()))
		for n := range Available() {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown remediation template %q (available: %s)", name, strings.Join(names, ", "))
	}
	data := buildData(ctx)
	out := map[string]string{}
	for format, ext := range formats {
		t, err := template.ParseFS(templatesFS, fmt.Sprintf("templates/%s.%s.tmpl", name, ext))
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		if err := t.Execute(&b, data); err != nil {
			return nil, fmt.Errorf("template %s (%s): %w", name, format, err)
		}
		out[format] = b.String()
	}
	return out, nil
}

// Available lists the embedded template names.
func Available() map[string]bool {
	entries, err := templatesFS.ReadDir("templates")
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		name = strings.TrimSuffix(name, ".tmpl")
		if i := strings.LastIndex(name, "."); i > 0 {
			out[name[:i]] = true
		}
	}
	return out
}

// tmplData is the flattened, pre-formatted view templates consume.
type tmplData struct {
	Service     string
	SafeService string // river block labels allow [a-zA-Z0-9_] only
	RuleID      string
	Metric      string
	Attribute   string
	Cardinality string
	RatioPct    string
}

func buildData(ctx Context) tmplData {
	d := tmplData{
		Service:     ctx.Service,
		SafeService: sanitize(ctx.Service),
		RuleID:      ctx.Finding.RuleID,
		// placeholders keep rendered patches valid when evidence lacks detail
		Metric:    "REPLACE_WITH_METRIC_NAME",
		Attribute: "REPLACE_WITH_ATTRIBUTE_KEY",
	}
	if ctx.Finding.Stats.Ratio > 0 {
		d.RatioPct = fmt.Sprintf("%.0f%%", ctx.Finding.Stats.Ratio*100)
	}
	for _, e := range ctx.Finding.Evidence {
		if e.Kind != "aggregate" {
			continue
		}
		if m, ok := e.Attrs["metric"].(string); ok && m != "" {
			d.Metric = m
		}
		if a, ok := e.Attrs["attribute"].(string); ok && a != "" && a != "span.name" {
			d.Attribute = a
		}
		if c, ok := e.Attrs["cardinality"].(float64); ok {
			d.Cardinality = fmt.Sprintf("%.0f", c)
		}
		break
	}
	return d
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "service"
	}
	return b.String()
}
