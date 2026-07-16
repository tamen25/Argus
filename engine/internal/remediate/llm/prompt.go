package llm

import (
	"embed"
	"strings"
	"text/template"
)

//go:embed prompts/*.tmpl
var promptFS embed.FS

// explainTmpl is parsed once; template rendering is deterministic, so it is the
// only part of the LLM path with golden tests (model output is not tested).
var explainTmpl = template.Must(template.ParseFS(promptFS, "prompts/explain.tmpl"))

// RenderPrompt builds the explanation prompt from a finding. It does NOT
// redact — callers pass an already-redacted input (Redact) so the redaction
// boundary is explicit and testable on its own.
func RenderPrompt(in ExplainInput) (string, error) {
	var b strings.Builder
	if err := explainTmpl.Execute(&b, in); err != nil {
		return "", err
	}
	return b.String(), nil
}
