package orchestrator

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/tamen25/Argus/engine/internal/bench/agent"
)

// RedactedValue replaces a literal removed from a shared report.
const RedactedValue = "…"

var (
	// A quoted literal in PromQL, LogQL or TraceQL: "…", '…' or `…`, with
	// backslash escapes inside double and single quotes.
	quotedLiteral = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'|`[^`]*`")
	// The query string of a URL in an error message (a backend's request
	// line carries the query, URL-encoded, so quoting rules do not apply).
	urlQuery = regexp.MustCompile(`\?[^\s"'<>]*`)
)

// redactText removes what a query or an error quotes from the environment:
// literal values and URL query strings. Label names, operators, functions
// and durations stay, so a reader can still see how an agent queried. A
// double-quoted string followed by a colon is a JSON key in an error body
// (a field name, not a value) and stays.
func redactText(s string) string {
	s = urlQuery.ReplaceAllString(s, "?"+RedactedValue)
	var b strings.Builder
	last := 0
	for _, m := range quotedLiteral.FindAllStringIndex(s, -1) {
		b.WriteString(s[last:m[0]])
		lit := s[m[0]:m[1]]
		if lit[0] == '"' && strings.HasPrefix(strings.TrimLeft(s[m[1]:], " "), ":") {
			b.WriteString(lit)
		} else {
			b.WriteString(lit[:1] + RedactedValue + lit[:1])
		}
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// Tool arguments by what they hold. Queries keep their shape and lose their
// literals; times, limits and alert states are not environment data; every
// other string argument (a namespace, a label, a name to match) is itself a
// value from the environment and is replaced whole.
var (
	queryArguments = map[string]bool{"query": true, "selector": true}
	plainArguments = map[string]bool{"start": true, "end": true, "time": true, "step": true, "limit": true, "state": true}
)

// redactArguments redacts the string values of a tool call's JSON
// arguments; arguments that are not a JSON object are redacted as text.
func redactArguments(args string) string {
	if args == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(args), &obj); err != nil {
		return redactText(args)
	}
	for k, v := range obj {
		s, ok := v.(string)
		switch {
		case !ok || s == "" || plainArguments[k]:
		case queryArguments[k]:
			obj[k] = redactText(s)
		default:
			obj[k] = RedactedValue
		}
	}
	b, err := json.Marshal(obj)
	if err != nil { // unreachable: obj came from json.Unmarshal
		return redactText(args)
	}
	return string(b)
}

// RedactReport returns a copy of rep fit to share outside the team that ran
// it. A tool log quotes the agent's queries, and errors quote backend
// requests; both carry literal values from the environment (service and
// tenant names, IDs, log text). Truncation would not remove them; this
// replaces each literal and URL query string with RedactedValue and marks
// the report Redacted. The structure of each query stays readable.
func RedactReport(rep Report) Report {
	out := rep
	out.Redacted = true
	out.Runs = make([]RunRecord, len(rep.Runs))
	for i, run := range rep.Runs {
		r := run
		r.Error = redactText(run.Error)
		r.CleanupError = redactText(run.CleanupError)
		if run.ToolLog != nil {
			r.ToolLog = make([]agent.ToolCall, len(run.ToolLog))
			for j, c := range run.ToolLog {
				c.Arguments = redactArguments(c.Arguments)
				c.Error = redactText(c.Error)
				r.ToolLog[j] = c
			}
		}
		out.Runs[i] = r
	}
	return out
}
