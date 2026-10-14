package backtest

import (
	"fmt"
	"time"
)

// MaxSteps bounds how many instants one walk over a window may evaluate: a
// presence sweep, or one rule's replay through one segment. Each is a backend
// query, so a tiny step over a long window would otherwise send millions of
// them. 100,000 covers 30 days at a 1-minute step with room to spare.
const MaxSteps = 100_000

// checkStepping validates a walk from from to to in steps of step: the step
// must be positive (a zero step never moves forward, and a negative one walks
// away from to forever), and the walk at most MaxSteps long. name is the
// step's name in the error, as the caller's user knows it.
func checkStepping(name string, from, to time.Time, step time.Duration) error {
	if step <= 0 {
		return fmt.Errorf("%s must be positive, got %s", name, step)
	}
	if n := to.Sub(from) / step; n > MaxSteps {
		return fmt.Errorf("a %s of %s over %s is %d evaluations, more than the limit of %d: use a larger %s or a shorter window",
			name, step, to.Sub(from), n, MaxSteps, name)
	}
	return nil
}
