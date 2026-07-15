-- Cost showback history (Phase 2): one row per priced cost report, for
-- week-over-week trends. The full report is kept as JSONB (line/storage
-- breakdowns are already bounded); top-level columns stay queryable.
CREATE TABLE cost_snapshots (
    id            BIGSERIAL PRIMARY KEY,
    taken_at      TIMESTAMPTZ NOT NULL,
    currency      TEXT NOT NULL,
    total_monthly DOUBLE PRECISION NOT NULL,
    payload       JSONB NOT NULL
);

CREATE INDEX cost_snapshots_taken_at_idx ON cost_snapshots (taken_at DESC);
