package backtest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// countingBackend answers every query and counts them.
type countingBackend struct{ calls int }

func (c *countingBackend) HasData(context.Context, time.Time) (bool, error) {
	c.calls++
	return true, nil
}

func (c *countingBackend) Eval(context.Context, string, time.Time) (map[string]float64, error) {
	c.calls++
	return nil, nil
}

// R09: a zero step never moves forward and a negative one walks away from
// the end, so either would query the backend forever. Every entry point
// refuses them, and a walk too long for its step, before any query.
func TestSteppingIsValidatedBeforeAnyQuery(t *testing.T) {
	ctx := context.Background()
	from := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	rule := backtest.Rule{Name: "R", Alert: true, Expr: "up == 0"}
	rs := backtest.RuleSet{Groups: []backtest.Group{{Name: "g", Rules: []backtest.Rule{rule}}}}

	cases := map[string]func(b *countingBackend) error{
		"Run, zero step": func(b *countingBackend) error {
			_, err := backtest.Run(ctx, b, b, backtest.RunInput{Rules: rs, From: from, To: to})
			return err
		},
		"Run, negative grace": func(b *countingBackend) error {
			_, err := backtest.Run(ctx, b, b, backtest.RunInput{Rules: rs, From: from, To: to, Step: time.Minute, Grace: -time.Minute})
			return err
		},
		"Run, too many steps": func(b *countingBackend) error {
			_, err := backtest.Run(ctx, b, b, backtest.RunInput{Rules: rs, From: from, To: from.Add(365 * 24 * time.Hour), Step: time.Second})
			return err
		},
		"Segments, negative stride": func(b *countingBackend) error {
			_, err := backtest.Segments(ctx, b, from, to, -time.Minute)
			return err
		},
		"Replay, zero step": func(b *countingBackend) error {
			_, err := backtest.Replay(ctx, b, rule, backtest.Segment{Start: from, End: to}, 0)
			return err
		},
		"UsableWindow, zero resolution": func(b *countingBackend) error {
			_, err := backtest.UsableWindow(ctx, b, from, to, 0)
			return err
		},
	}
	for name, call := range cases {
		b := &countingBackend{}
		err := call(b)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if b.calls != 0 {
			t.Errorf("%s: %d backend queries before the error, want none", name, b.calls)
		}
		if !strings.Contains(err.Error(), "must") && !strings.Contains(err.Error(), "limit") {
			t.Errorf("%s: error %q does not say what is wrong", name, err)
		}
	}
}
