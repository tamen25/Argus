package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench/agent"
)

// TestRedactReportRemovesLiteralsButKeepsQueryShape is R29's remaining part:
// a report's tool log quotes the agent's queries and the errors backends
// returned, which carry values from the environment (service names, tenant
// IDs, log text). Truncation is not redaction. A shared report must lose the
// values but keep what makes it useful: which tool, which labels and
// functions, and that a call failed.
func TestRedactReportRemovesLiteralsButKeepsQueryShape(t *testing.T) {
	rep := Report{Runs: []RunRecord{{
		Error:        `agent: finished without submitting (last reply: "tenant acme-corp looks fine")`,
		CleanupError: `restore failed for "payment-7549d9dd44"`,
		ToolLog: []agent.ToolCall{
			{
				Tool:      "query_prometheus",
				Arguments: `{"query":"sum(rate(http_requests_total{job=\"checkout\",tenant='acme-corp'}[5m]))","start":"2026-11-05T13:00:00Z"}`,
			},
			{
				Tool:      "query_loki",
				Arguments: "{\"query\":\"{service_name=\\\"ledger\\\"} |= `card 4111111111111111`\"}",
				Error:     `backend http://127.0.0.1:3100/loki/api/v1/query_range?query=%7Bservice_name%3D%22ledger%22%7D&limit=100: HTTP 400`,
			},
		},
	}}}

	got := RedactReport(rep)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	all := string(b)
	for _, secret := range []string{"checkout", "acme-corp", "ledger", "4111111111111111", "payment-7549d9dd44", "%7Bservice_name"} {
		if strings.Contains(all, secret) {
			t.Errorf("redacted report still contains %q:\n%s", secret, all)
		}
	}
	for _, kept := range []string{"query_prometheus", "http_requests_total", "rate(", "job=", "[5m]", "service_name=", "|=", "2026-11-05T13:00:00Z", "HTTP 400"} {
		if !strings.Contains(all, kept) {
			t.Errorf("redaction removed %q, which shows how the agent queried:\n%s", kept, all)
		}
	}
	if !got.Redacted {
		t.Error("a redacted report must say so")
	}
	if !strings.Contains(rep.Runs[0].ToolLog[0].Arguments, "checkout") {
		t.Error("RedactReport must not modify the report it was given")
	}
}

func TestRedactArgumentsThatAreNotJSON(t *testing.T) {
	if got := redactArguments(`up{job="cart"}`); got != `up{job="…"}` {
		t.Errorf("got %q", got)
	}
	if got := redactArguments(""); got != "" {
		t.Errorf("empty arguments became %q", got)
	}
}

// TestRedactPlainArgumentsAndKeepErrorKeys: a namespace or a label passed as
// a plain argument is environment data too; an error body's JSON keys are
// field names and stay readable.
func TestRedactPlainArgumentsAndKeepErrorKeys(t *testing.T) {
	got := redactArguments(`{"namespace":"cloudops-mesh","state":"firing","limit":20,"match":"http_"}`)
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatal(err)
	}
	if m["namespace"] != RedactedValue || m["match"] != RedactedValue {
		t.Errorf("plain values not redacted: %s", got)
	}
	if m["state"] != "firing" || m["limit"] != float64(20) {
		t.Errorf("a state or limit is not environment data: %s", got)
	}
	e := redactText(`HTTP 400: {"status":"error","errorType":"bad_data","error":"bad selector {job=\"cart\"}"}`)
	if !strings.Contains(e, `"status":`) || !strings.Contains(e, `"errorType":`) || strings.Contains(e, "cart") {
		t.Errorf("got %s", e)
	}
}
