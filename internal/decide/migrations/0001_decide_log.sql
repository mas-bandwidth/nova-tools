-- 0001_decide_log.sql is the decision and escalation log as a durable table,
-- applied by `nova-decide log migrate --dsn-env <NAME>`. It sits in the same
-- database as card_results (docs/SPEC-STATE.md, "the record: card results"), so
-- a decision and the card result it produced are one join away.
--
-- It is plain SQL executed statement by statement in one transaction and every
-- statement is idempotent: the tables and indexes are IF NOT EXISTS and the
-- version is inserted ON CONFLICT DO NOTHING, so the migration may be run on
-- every start and only ever installs itself once.

CREATE TABLE IF NOT EXISTS decide_schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS decide_log (
    id                   BIGSERIAL PRIMARY KEY,
    -- when the decision was made, and what it was about
    ts                   TIMESTAMPTZ NOT NULL,
    unit_id              TEXT NOT NULL,
    kind                 TEXT NOT NULL,
    -- the size buckets: the evidence's own measure of how big the unit is
    files                INTEGER NOT NULL DEFAULT 0,
    packages             INTEGER NOT NULL DEFAULT 0,
    lanes                INTEGER NOT NULL DEFAULT 0,
    lane                 TEXT NOT NULL DEFAULT '',
    -- the rung it tried, and the confidence it was gated on
    rung_tried           TEXT NOT NULL DEFAULT '',
    height               INTEGER NOT NULL DEFAULT 0,
    confidence           DOUBLE PRECISION NOT NULL DEFAULT 0,
    floor                DOUBLE PRECISION NOT NULL DEFAULT 0,
    stepped_up           BOOLEAN NOT NULL DEFAULT FALSE,
    escalated            BOOLEAN NOT NULL DEFAULT FALSE,
    designated           BOOLEAN NOT NULL DEFAULT FALSE,
    source               TEXT NOT NULL DEFAULT '',
    rowan_pick           TEXT NOT NULL DEFAULT '',
    reason               TEXT NOT NULL DEFAULT '',
    -- the typed action beside the rung, and why a route ended in a refusal
    wait                 TEXT NOT NULL DEFAULT '',
    awaiting_termination BOOLEAN NOT NULL DEFAULT FALSE,
    refusal              TEXT NOT NULL DEFAULT '',
    -- what followed, filled in by the caller that watched the work
    outcome              TEXT NOT NULL DEFAULT '',
    rung_succeeded       TEXT NOT NULL DEFAULT '',
    -- what the decision spent. tokens_in and tokens_out are NULL where the
    -- provider did not report THAT counter: an absence is not a zero
    -- (SPEC-TOKENS rule 14, Stella's presence rule).
    calls                INTEGER NOT NULL DEFAULT 0,
    tokens_in            BIGINT,
    tokens_out           BIGINT,
    usage_failed         BOOLEAN NOT NULL DEFAULT FALSE,
    -- the whole evidence, so the summary reads the same rows off either sink
    evidence             JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS decide_log_kind_idx ON decide_log (kind);

CREATE INDEX IF NOT EXISTS decide_log_ts_idx ON decide_log (ts DESC);

CREATE INDEX IF NOT EXISTS decide_log_unit_idx ON decide_log (unit_id);

INSERT INTO decide_schema_version (version) VALUES (1) ON CONFLICT (version) DO NOTHING;
