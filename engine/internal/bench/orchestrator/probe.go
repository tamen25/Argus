package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
)

// InstantQuerier is the narrow slice of a metrics backend the steady-state probe
// needs. Declared here rather than imported from a client package so the
// orchestrator depends on the capability, not on Mimir (architecture rule 1).
type InstantQuerier interface {
	QueryInstant(ctx context.Context, query string, at time.Time) (json.RawMessage, error)
}

// PromQLProbe waits until the scenario's telemetry condition holds — the fault
// is not merely applied but *observable*. Those are different moments: metrics
// reach a backend a scrape interval or more after the workload changes, and an
// agent asked in that gap sees a healthy system and is scored on it.
//
// A scenario with no steadyState block is treated as immediately ready, matching
// the previous behavior, and the report is expected to say so.
type PromQLProbe struct {
	Q InstantQuerier
	// Now is injectable for tests.
	Now func() time.Time

	// firstHeld records when the condition first held, for the settle wait.
	firstHeld time.Time
}

// Reached evaluates the scenario's steady-state query.
func (p *PromQLProbe) Reached(ctx context.Context, sc bench.Scenario) (bool, error) {
	ss := sc.Spec.SteadyState
	if ss == nil {
		return true, nil
	}
	if p.Q == nil {
		return false, fmt.Errorf("scenario %q declares steadyState but no metrics backend is configured "+
			"(pass --mimir-url)", sc.Metadata.Name)
	}
	now := p.now()

	raw, err := p.Q.QueryInstant(ctx, ss.Query, now)
	if err != nil {
		// A backend that is not answering yet is a not-yet, not a failure: during
		// bootstrap the query legitimately fails before the first scrape lands.
		return false, nil
	}
	v, ok, err := firstSampleValue(raw)
	if err != nil {
		return false, fmt.Errorf("steady-state query %q: %w", ss.Query, err)
	}
	if !ok {
		return false, nil // empty result: the series does not exist yet
	}

	if ss.Min != nil && v < *ss.Min {
		p.firstHeld = time.Time{}
		return false, nil
	}
	if ss.Max != nil && v > *ss.Max {
		p.firstHeld = time.Time{}
		return false, nil
	}

	// Condition holds. Hold it for the settle window before declaring steady, so
	// a value that has only just crossed the line is not mistaken for an
	// established state.
	settle, err := ss.SettleDur()
	if err != nil {
		return false, err
	}
	if settle <= 0 {
		return true, nil
	}
	if p.firstHeld.IsZero() {
		p.firstHeld = now
		return false, nil
	}
	return !now.Before(p.firstHeld.Add(settle)), nil
}

func (p *PromQLProbe) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// firstSampleValue pulls the first numeric sample out of a Prometheus instant
// query response, accepting both vector and scalar result types. Reports
// (value, present, error): an empty vector is "not present", not an error, since
// a series that does not exist yet is the normal pre-fault state.
func firstSampleValue(raw json.RawMessage) (float64, bool, error) {
	var resp struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, false, fmt.Errorf("decoding response: %w", err)
	}
	if resp.Status != "" && resp.Status != "success" {
		return 0, false, fmt.Errorf("query status %q", resp.Status)
	}

	switch resp.Data.ResultType {
	case "scalar":
		var pair []any
		if err := json.Unmarshal(resp.Data.Result, &pair); err != nil {
			return 0, false, fmt.Errorf("decoding scalar: %w", err)
		}
		return samplePair(pair)
	default: // vector, and anything else shaped like one
		var vec []struct {
			Value []any `json:"value"`
		}
		if err := json.Unmarshal(resp.Data.Result, &vec); err != nil {
			return 0, false, fmt.Errorf("decoding vector: %w", err)
		}
		if len(vec) == 0 {
			return 0, false, nil
		}
		return samplePair(vec[0].Value)
	}
}

// samplePair reads the [timestamp, "value"] pair Prometheus uses, where the
// value is a JSON string rather than a number.
func samplePair(pair []any) (float64, bool, error) {
	if len(pair) < 2 {
		return 0, false, fmt.Errorf("sample has %d fields, want [timestamp, value]", len(pair))
	}
	switch v := pair[1].(type) {
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, false, fmt.Errorf("parsing sample value %q: %w", v, err)
		}
		return f, true, nil
	case float64:
		return v, true, nil
	default:
		return 0, false, fmt.Errorf("sample value has unexpected type %T", pair[1])
	}
}

var _ SteadyStateProbe = (*PromQLProbe)(nil)
