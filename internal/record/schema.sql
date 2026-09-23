-- schema.sql is the durable record for card results, applied by `nova-work record --migrate`.
-- It is plain SQL executed statement by statement in one transaction and is idempotent:
-- every table is IF NOT EXISTS and the version is inserted ON CONFLICT DO NOTHING, so the
-- migration may be run on every start and only ever installs itself once.

CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS card_results (
    stream_id   TEXT PRIMARY KEY,
    label       TEXT NOT NULL,
    bench       TEXT NOT NULL,
    exit        INTEGER NOT NULL,
    result_line TEXT NOT NULL DEFAULT '',
    job_path    TEXT NOT NULL DEFAULT '',
    commit      TEXT NOT NULL DEFAULT '',
    branch      TEXT NOT NULL DEFAULT '',
    pr          TEXT,
    pushed_at   TIMESTAMPTZ,
    done_at     TIMESTAMPTZ,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS card_results_bench_idx ON card_results (bench);

CREATE INDEX IF NOT EXISTS card_results_done_at_idx ON card_results (done_at DESC);

INSERT INTO schema_version (version) VALUES (1) ON CONFLICT (version) DO NOTHING;

-- token_ledger, the index of nova-tokens' day TSVs, keyed exactly (day, card, model, repo).
-- A token type no source reported is NULL, never 0. The table is additive and IF NOT EXISTS,
-- so it rides schema version 1: an existing database gains it on the next --migrate.
CREATE TABLE IF NOT EXISTS token_ledger (
    day           DATE   NOT NULL,
    card          TEXT   NOT NULL,
    model         TEXT   NOT NULL,
    repo          TEXT   NOT NULL,
    provider      TEXT   NOT NULL DEFAULT '',
    input_tokens  BIGINT,
    output_tokens BIGINT,
    cache_read    BIGINT,
    cache_write   BIGINT,
    reasoning     BIGINT,
    rough         INTEGER NOT NULL DEFAULT 0,
    sources       TEXT   NOT NULL DEFAULT '',
    PRIMARY KEY (day, card, model, repo)
);
