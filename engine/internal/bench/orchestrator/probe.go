package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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

// PromQLProbe reads the scenario's steady-state query against a metrics
// backend. It answers one question — does the fault's signature hold RIGHT NOW —
// and keeps no state between calls.
//
// That statelessness is deliberate. An earlier version tracked the settle window
// inside the probe, and since one probe serves every repeat of a run, repeat 2
// inherited repeat 1's timer and skipped its settle window entirely. All timing
// (settle, baseline wait, deadlines) now lives in the orchestrator, in per-call
// local state that cannot leak between repeats.
//
// A scenario with no steadyState block is treated as immediately ready, and the
// report is expected to say steady state was not verified.
type PromQLProbe struct {
	Q InstantQuerier
}

// signature is what one read of the steady-state query found.
type signature int

const (
	// unknown: the backend did not answer, so neither "present" nor "absent"
	// can be claimed. Early in a run this is normal (no scrape has landed yet).
	unknown signature = iota
	// absent: the query answered and the fault's condition does not hold.
	absent
	// present: the query answered and the fault's condition holds.
	present
)

// read evaluates the steady-state query once and classifies the result.
func (p *PromQLProbe) read(ctx context.Context, sc bench.Scenario) (signature, error) {
	ss := sc.Spec.SteadyState
	if p.Q == nil {
		return unknown, fmt.Errorf("scenario %q declares steadyState but no metrics backend is configured "+
			"(pass --mimir-url)", sc.Metadata.Name)
	}
	raw, err := p.Q.QueryInstant(ctx, ss.Query, time.Now())
	if err != nil {
		return unknown, nil // backend not answering yet: a not-yet, not a failure
	}
	v, ok, err := firstSampleValue(raw)
	if err != nil {
		return unknown, fmt.Errorf("steady-state query %q: %w", ss.Query, err)
	}
	if !ok {
		return absent, nil // empty result: the series does not exist
	}
	// histogram_quantile returns NaN when no requests fell in the window. NaN
	// fails every comparison, so it would otherwise read as "present": a gate
	// passed with no fault, a baseline that can never be proven clean. It is no
	// evidence either way.
	if math.IsNaN(v) {
		return unknown, nil
	}
	if (ss.Min != nil && v < *ss.Min) || (ss.Max != nil && v > *ss.Max) {
		return absent, nil
	}
	return present, nil
}

// Reached reports whether the fault's signature holds right now. It does not
// apply the settle window; the orchestrator does.
func (p *PromQLProbe) Reached(ctx context.Context, sc bench.Scenario) (bool, error) {
	if sc.Spec.SteadyState == nil {
		return true, nil
	}
	sig, err := p.read(ctx, sc)
	return sig == present, err
}

// Clean reports whether the fault's signature is verifiably absent — the state a
// repeat must start from for its result to be independent of the previous one.
// An unanswered query is NOT clean: absence has to be observed, not assumed.
func (p *PromQLProbe) Clean(ctx context.Context, sc bench.Scenario) (bool, error) {
	if sc.Spec.SteadyState == nil {
		return true, nil
	}
	sig, err := p.read(ctx, sc)
	return sig == absent, err
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

var (
	_ SteadyStateProbe = (*PromQLProbe)(nil)
	_ BaselineProbe    = (*PromQLProbe)(nil)
)
