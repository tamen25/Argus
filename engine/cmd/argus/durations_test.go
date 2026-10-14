package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// R09: a zero --score-interval used to reach time.NewTicker, which panics in
// a goroutine and takes the engine down. serve now refuses it at start, and
// names every bad flag at once.
func TestServeRejectsBadDurations(t *testing.T) {
	good := serveConfig{
		interval: 30 * time.Second, window: time.Hour,
		cost:     serveCostConfig{window: time.Hour},
		backtest: serveBacktestConfig{window: time.Hour, step: time.Minute},
	}
	if err := good.validate(); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}

	bad := good
	bad.interval = 0
	bad.backtest.step = -time.Minute
	bad.cost.cacheTTL = -time.Second
	err := serve(context.Background(), bad)
	if err == nil {
		t.Fatal("serve started with a zero score interval")
	}
	for _, flag := range []string{"--score-interval", "--backtest-step", "--cost-cache-ttl"} {
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("error %q does not name %s", err, flag)
		}
	}
}

// The one-shot commands check their durations before loading anything or
// calling a backend: none of these calls has a backend to reach.
func TestCommandsRejectBadDurations(t *testing.T) {
	ctx := context.Background()
	cases := map[string]error{}
	_, cases["backtest run --step 0"] = runBacktestRun(ctx, &backtestRunOptions{grace: time.Minute})
	_, cases["backtest run --grace -1m"] = runBacktestRun(ctx, &backtestRunOptions{step: time.Minute, grace: -time.Minute})
	_, cases["backtest diff --step 0"] = runBacktestDiff(ctx, &backtestDiffOptions{})
	_, cases["backtest replay --step 0"] = runBacktestReplay(ctx, &backtestReplayOptions{})
	_, cases["cost --window 0"] = runCost(ctx, &costOptions{})
	_, cases["score --window -1s"] = runScore(ctx, &scoreOptions{window: -time.Second})
	for name, err := range cases {
		if err == nil || !strings.Contains(err.Error(), "must") {
			t.Errorf("%s: err = %v, want the bad flag named", name, err)
		}
	}
}
