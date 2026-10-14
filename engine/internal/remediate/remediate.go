// Package remediate renders deterministic configuration patches for
// findings: Alloy (River) and OTel Collector YAML a human reviews and
// applies. Read-only product (architecture rule 5) — Argus generates files,
// never touches user infrastructure. LLM-drafted explanation text lives in
// remediate/llm; nothing here may import it.
package remediate

import (
	"embed"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/tamen25/Argus/engine/internal/model"
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

// funcs are the functions templates may call. q quotes a telemetry value for
// a comment, Go-style, on one line: a value holding a newline cannot end the
// comment and start a configuration line.
var funcs = template.FuncMap{"q": strconv.Quote}

// parsed holds every embedded template, parsed once: name → format → template.
// The templates are compiled into the binary, so a parse error is a bug in
// this package, and the tests catch it.
var parsed = func() map[string]map[string]*template.Template {
	out := map[string]map[string]*template.Template{}
	entries, err := templatesFS.ReadDir("templates")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		// "<name>.<ext>.tmpl"
		base := strings.TrimSuffix(e.Name(), ".tmpl")
		dot := strings.LastIndex(base, ".")
		name, ext := base[:dot], base[dot+1:]
		t := template.Must(template.New(e.Name()).Funcs(funcs).ParseFS(templatesFS, "templates/"+e.Name()))
		if out[name] == nil {
			out[name] = map[string]*template.Template{}
		}
		out[name][ext] = t
	}
	return out
}()

// Render produces every output format for one template. Output is
// deterministic: same context, same bytes.
func Render(name string, ctx Context) (map[string]string, error) {
	byExt, ok := parsed[name]
	if !ok {
		names := make([]string, 0, len(parsed))
		for n := range parsed {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown remediation template %q (available: %s)", name, strings.Join(names, ", "))
	}
	data := buildData(ctx)
	out := map[string]string{}
	for format, ext := range formats {
		var b strings.Builder
		if err := byExt[ext].Execute(&b, data); err != nil {
			return nil, fmt.Errorf("template %s (%s): %w", name, format, err)
		}
		out[format] = b.String()
	}
	return out, nil
}

// Available lists the embedded template names.
func Available() map[string]bool {
	out := make(map[string]bool, len(parsed))
	for name := range parsed {
		out[name] = true
	}
	return out
}

// Placeholders stand in for a value the finding does not carry, or one that
// cannot be written into configuration safely. A patch holding one does not
// apply as it is: the reviewer has to fill it in.
const (
	placeholderMetric    = "REPLACE_WITH_METRIC_NAME"
	placeholderAttribute = "REPLACE_WITH_ATTRIBUTE_KEY"
	placeholderService   = "REPLACE_WITH_SERVICE_NAME"
)

// configSafe matches names that can be written inside a quoted OTTL string,
// itself inside an Alloy raw string or a YAML plain scalar, without escaping:
// the character set OpenTelemetry allows in instrument names, plus ':' for
// Prometheus-style names. Metric names and attribute keys come from
// telemetry, so anything else (a quote, a backtick, a newline) is not written
// into configuration at all.
var configSafe = regexp.MustCompile(`^[A-Za-z0-9_.:/-]+$`)

// tmplData is the flattened, pre-formatted view templates consume.
type tmplData struct {
	Service     string // the service as observed, for comments (quoted with q)
	SafeService string // river block labels allow [a-zA-Z0-9_] only
	// ScopeService is the service.name a patch restricts itself to, so it
	// cannot change other services' telemetry on a shared collector. It is a
	// placeholder when the service has no usable name.
	ScopeService string
	RuleID       string
	// Metric and Attribute are written into OTTL statements: config-safe
	// values or placeholders.
	Metric      string
	Attribute   string
	Cardinality string
	RatioPct    string
}

func buildData(ctx Context) tmplData {
	d := tmplData{
		Service:      ctx.Service,
		SafeService:  sanitize(ctx.Service),
		ScopeService: placeholderService,
		RuleID:       ctx.Finding.RuleID,
		Metric:       placeholderMetric,
		Attribute:    placeholderAttribute,
	}
	// A service without a real name cannot be selected by name: the reviewer
	// has to say which telemetry the patch is for.
	if configSafe.MatchString(ctx.Service) && ctx.Service != model.UnknownService &&
		!strings.HasPrefix(ctx.Service, "unknown_service") {
		d.ScopeService = ctx.Service
	}
	if ctx.Finding.Stats.Ratio > 0 {
		d.RatioPct = fmt.Sprintf("%.0f%%", ctx.Finding.Stats.Ratio*100)
	}
	for _, e := range ctx.Finding.Evidence {
		if e.Kind != "aggregate" {
			continue
		}
		if m, ok := e.Attrs["metric"].(string); ok && configSafe.MatchString(m) {
			d.Metric = m
		}
		if a, ok := e.Attrs["attribute"].(string); ok && a != "span.name" && configSafe.MatchString(a) {
			d.Attribute = a
		}
		// The count is a float64 when the finding was read from JSON and an
		// int64 when it comes straight from the engine.
		if c, ok := rules.AsFloat(e.Attrs["cardinality"]); ok {
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
