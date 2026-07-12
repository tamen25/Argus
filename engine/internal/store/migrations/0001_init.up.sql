-- Argus score history schema (Phase 1: snapshots, per-service scores, findings).
CREATE TABLE score_snapshots (
    id            BIGSERIAL PRIMARY KEY,
    taken_at      TIMESTAMPTZ NOT NULL,
    fleet_score   DOUBLE PRECISION NOT NULL,
    spec_version  TEXT NOT NULL,
    argus_version TEXT NOT NULL
);

CREATE TABLE service_scores (
    snapshot_id     BIGINT NOT NULL REFERENCES score_snapshots(id) ON DELETE CASCADE,
    service         TEXT NOT NULL,
    spec_score      DOUBLE PRECISION NOT NULL,
    extension_score DOUBLE PRECISION,
    category        TEXT NOT NULL,
    PRIMARY KEY (snapshot_id, service)
);

CREATE TABLE findings (
    id          BIGSERIAL PRIMARY KEY,
    snapshot_id BIGINT NOT NULL REFERENCES score_snapshots(id) ON DELETE CASCADE,
    service     TEXT NOT NULL,
    rule_id     TEXT NOT NULL,
    source      TEXT NOT NULL,
    impact      TEXT NOT NULL,
    confidence  TEXT NOT NULL,
    observed    BIGINT NOT NULL,
    violations  BIGINT NOT NULL,
    ratio       DOUBLE PRECISION NOT NULL,
    -- full finding (evidence samples are already bounded and truncated
    -- upstream; raw telemetry is never persisted)
    payload     JSONB NOT NULL
);

CREATE INDEX findings_service_rule_idx ON findings (service, rule_id);
CREATE INDEX service_scores_service_idx ON service_scores (service);
