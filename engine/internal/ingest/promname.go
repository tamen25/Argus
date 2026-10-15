package ingest

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// An OTLP metric reaches Mimir under a Prometheus name, not its own. The
// translation (Prometheus' otlptranslator, used by the Collector's and Alloy's
// remote-write exporters and by Mimir's OTLP endpoint) splits the name on
// anything but letters and digits and joins the parts with "_", so
// "http.server.duration" becomes "http_server_duration". With suffixes on,
// the default for the remote-write exporters, it then appends the unit
// ("_milliseconds"), "_total" for a counter and "_ratio" for a gauge in "1".
// Mimir's own OTLP endpoint adds no suffixes unless configured to, so the
// poller accepts either name. A histogram is stored as _bucket, _count and
// _sum series.

// promNameRegexp returns a regular expression for __name__ that matches the
// series of an OTLP metric whichever translation the pipeline applied. It is
// an alternation of exact names, so it cannot match another metric.
func promNameRegexp(name, unit, typ string) string {
	tokens := nameTokens(name)
	plain := strings.Join(tokens, "_")
	names := []string{plain}
	if suffixed := withSuffixes(tokens, unit, typ); suffixed != plain {
		names = append(names, suffixed)
	}
	// A sum may be monotonic or not, and only a monotonic one is a counter
	// with "_total". The item does not say which, so both are accepted.
	if typ == "sum" {
		names = append(names, withSuffixes(tokens, unit, ""))
	}
	slices.Sort(names)
	names = slices.Compact(names)

	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	re := "(" + strings.Join(quoted, "|") + ")"
	switch typ {
	case "histogram":
		return re + "_(bucket|count|sum)"
	case "exponential_histogram":
		// A native histogram keeps the name; one converted to classic
		// buckets gets the suffixes.
		return re + "(_bucket|_count|_sum)?"
	case "summary":
		return re + "(_count|_sum)?"
	}
	return re
}

// promLabelName is the label an OTLP attribute key becomes: every character
// but letters, digits and "_" turns into "_"; a key starting with a digit
// gets "key_" in front, and one starting with a single "_" gets "key".
func promLabelName(key string) string {
	var b strings.Builder
	for _, r := range key {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	label := b.String()
	switch {
	case label == "":
		return label
	case unicode.IsDigit(rune(label[0])):
		return "key_" + label
	case strings.HasPrefix(label, "_") && !strings.HasPrefix(label, "__"):
		return "key" + label
	}
	return label
}

// nameTokens splits a metric name on everything but letters and digits.
func nameTokens(name string) []string {
	tokens := strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(tokens) > 0 && unicode.IsDigit(rune(tokens[0][0])) {
		tokens[0] = "_" + tokens[0]
	}
	return tokens
}

// withSuffixes builds the suffixed name: the unit's words when the name does
// not already contain them, then "total" for a counter (moved to the end) or
// "ratio" for a gauge in "1".
func withSuffixes(tokens []string, unit, typ string) string {
	out := slices.Clone(tokens)
	if typ == "sum" {
		out = slices.DeleteFunc(out, func(t string) bool { return t == "total" })
	}
	main, per, _ := strings.Cut(unit, "/")
	if w := unitWord(main, mainUnits); w != "" && !slices.Contains(out, w) {
		out = append(out, w)
	}
	if w := unitWord(per, perUnits); w != "" && !slices.Contains(out, w) {
		out = append(out, "per", w)
	}
	switch {
	case typ == "sum":
		out = append(out, "total")
	case typ == "gauge" && main == "1" && !slices.Contains(out, "ratio"):
		out = append(out, "ratio")
	}
	return strings.Join(out, "_")
}

// unitWord is the word a UCUM unit is written as in a name. An annotation in
// braces ("{request}") and "1" add nothing; a unit not in the table is used
// as written, cleaned to letters and digits.
func unitWord(unit string, table map[string]string) string {
	unit = strings.TrimSpace(unit)
	if unit == "" || unit == "1" || strings.ContainsAny(unit, "{}") {
		return ""
	}
	if w, ok := table[unit]; ok {
		return w
	}
	return strings.Join(nameTokens(unit), "_")
}

var mainUnits = map[string]string{
	"d": "days", "h": "hours", "min": "minutes", "s": "seconds",
	"ms": "milliseconds", "us": "microseconds", "ns": "nanoseconds",
	"By": "bytes", "KiBy": "kibibytes", "MiBy": "mebibytes", "GiBy": "gibibytes", "TiBy": "tibibytes",
	"KBy": "kilobytes", "MBy": "megabytes", "GBy": "gigabytes", "TBy": "terabytes",
	"m": "meters", "V": "volts", "A": "amperes", "J": "joules", "W": "watts", "g": "grams",
	"Cel": "celsius", "Hz": "hertz", "%": "percent",
}

var perUnits = map[string]string{
	"s": "second", "m": "minute", "h": "hour", "d": "day", "w": "week", "mo": "month", "y": "year",
}
