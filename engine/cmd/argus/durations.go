package main

import (
	"fmt"
	"time"
)

// Duration flags are checked when a command starts, before it opens a
// listener, starts a goroutine or queries a backend. A zero or negative
// interval panics in time.NewTicker, and a zero step never moves a replay
// forward; both are a mistyped flag, and the error should say which one.
//
// Each check returns nil when the value is fine, so a command collects them
// with errors.Join, which reports every bad flag at once and is nil when all
// are fine.

// positive requires d > 0.
func positive(flag string, d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("--%s must be positive, got %s", flag, d)
	}
	return nil
}

// nonNegative requires d >= 0, for durations where zero means "none" (no
// caching, no grace margin, the adapter's default timeout).
func nonNegative(flag string, d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("--%s must not be negative, got %s", flag, d)
	}
	return nil
}
