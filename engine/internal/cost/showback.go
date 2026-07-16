package cost

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Showback is the full cost report: attribution, storage, lifecycle savings,
// and (when a prior snapshot exists) week-over-week movement. Rendered to
// Markdown for humans and JSON for machines/CI.
type Showback struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Window      string           `json:"window"`
	Report      Report           `json:"report"`
	Lifecycle   []Recommendation `json:"lifecycle,omitempty"`
	Trend       *TrendReport     `json:"trend,omitempty"`
	// Notes carry honesty caveats (modeled-not-billed, sampling, segmented
	// windows) into every rendering.
	Notes []string `json:"notes,omitempty"`
}

// RenderJSON renders the showback as indented JSON (CI-friendly, --output json).
func RenderJSON(s Showback) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// RenderMarkdown renders the showback for humans. Deterministic: the cost
// core already sorted every slice, so the same Showback is byte-identical.
func RenderMarkdown(s Showback) string {
	var b strings.Builder
	cur := s.Report.Currency
	fmt.Fprintf(&b, "# Argus Cost & Showback\n\n")
	fmt.Fprintf(&b, "- Generated: %s · window %s\n", s.GeneratedAt.Format(time.RFC3339), s.Window)
	fmt.Fprintf(&b, "- **Total: %s %s / month**\n\n", money(s.Report.TotalMonthly), cur)

	for _, n := range s.Notes {
		fmt.Fprintf(&b, "> %s\n\n", n)
	}

	fmt.Fprintf(&b, "## By service and signal\n\n")
	fmt.Fprintf(&b, "| Service | Signal | Team | Ingest | Active series | Total /mo |\n|---|---|---|---:|---:|---:|\n")
	for _, l := range s.Report.Lines {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			l.Service, l.Signal, dash(l.Team), money(l.IngestMonthly), money(l.ActiveSeriesMonthly), money(l.TotalMonthly))
	}

	if len(s.Report.Storage) > 0 {
		fmt.Fprintf(&b, "\n## Storage by class\n\n")
		fmt.Fprintf(&b, "| Class | GB | /mo |\n|---|---:|---:|\n")
		for _, st := range s.Report.Storage {
			fmt.Fprintf(&b, "| %s | %.1f | %s |\n", st.Class, st.GB, money(st.Monthly))
		}
	}

	if len(s.Lifecycle) > 0 {
		fmt.Fprintf(&b, "\n## Lifecycle savings\n\n")
		fmt.Fprintf(&b, "| Move | GB | Now /mo | After /mo | Save /mo |\n|---|---:|---:|---:|---:|\n")
		for _, r := range s.Lifecycle {
			fmt.Fprintf(&b, "| %s → %s | %.1f | %s | %s | **%s** |\n",
				r.FromClass, r.ToClass, r.GB, money(r.CurrentMonthly), money(r.ProjectedMonthly), money(r.SavingsMonthly))
		}
	}

	if s.Trend != nil && len(s.Trend.Lines) > 0 {
		fmt.Fprintf(&b, "\n## Week-over-week\n\n")
		fmt.Fprintf(&b, "- Total change: %s %s/mo\n\n", signedMoney(s.Trend.TotalDelta), cur)
		fmt.Fprintf(&b, "| Service | Signal | Was | Now | Δ | Δ%% |\n|---|---|---:|---:|---:|---:|\n")
		for _, l := range s.Trend.Lines {
			pct := "—"
			if l.Previous > 0 {
				pct = fmt.Sprintf("%+.0f%%", l.PercentDelta)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
				l.Service, l.Signal, money(l.Previous), money(l.Current), signedMoney(l.Delta), pct)
		}
	}

	return b.String()
}

func money(v float64) string       { return fmt.Sprintf("%.2f", v) }
func signedMoney(v float64) string { return fmt.Sprintf("%+.2f", v) }

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
