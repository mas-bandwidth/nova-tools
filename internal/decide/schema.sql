-- schema.sql is the OWNED, VERSIONED contract for the decisions table (rule 8's
-- calibration record). It is applied by `nova-decide migrate --dsn <dsn>`.
--
-- Ownership, established by search before a line of this was written: no
-- `decisions` DDL existed anywhere in the organisation. SPEC-DECIDE's "Postgres
-- — the decisions table" declared the shape in prose --
-- `(question_hash, kind, answer, provider_confidence, floor, outcome)` -- and
-- named no owner, no migration and no version; an org-wide code search for
-- `provider_confidence` returned four hits, all of them in this repository, and
-- the only writer and reader are internal/decide and cmd/nova-decide. This
-- repository also owns the org's one versioned migration pattern
-- (internal/record/schema.sql). So the home is here, and this file declares the
-- table rather than guessing at somebody else's DDL.
--
-- Its version counter is `decisions_schema_version` and NOT internal/record's
-- `schema_version`: the two stores are reached by different DSNs and must not
-- share a number.
--
-- Like internal/record/schema.sql this is plain SQL, executed statement by
-- statement in ONE transaction, and idempotent in both directions:
--
--   * a FRESH database gets the whole table, with `source` present and
--     `provider_confidence` nullable from the start;
--   * a database carrying the hand-made six-column shape is UPGRADED in place
--     by the two ALTERs, which are no-ops on a fresh table.
--
-- Version 1 is the first declared version of this table. A database with no
-- `decisions_schema_version` row is UNMIGRATED, whatever tables it happens to
-- carry, and the writer refuses it by name rather than degrading the row.

CREATE TABLE IF NOT EXISTS decisions_schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS decisions (
    question_hash       TEXT NOT NULL,
    kind                TEXT NOT NULL,
    answer              TEXT NOT NULL,
    -- NULL on purpose, and the reason this migration exists: a decision the
    -- machinery settled cost no provider call, so there is no calibration
    -- evidence in the row. A zero or a display constant written here would
    -- become exactly that.
    provider_confidence DOUBLE PRECISION,
    floor               DOUBLE PRECISION NOT NULL,
    outcome             TEXT NOT NULL DEFAULT '',
    -- Which of the two decided: 'machinery' or 'provider'. Empty in a row
    -- written before this column existed, which is an absence and not a claim.
    source              TEXT NOT NULL DEFAULT ''
);

-- The upgrade path for a table created by hand in the six-column shape. Both
-- statements are no-ops on a table the CREATE above just made.
ALTER TABLE decisions ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT '';

ALTER TABLE decisions ALTER COLUMN provider_confidence DROP NOT NULL;

CREATE INDEX IF NOT EXISTS decisions_kind_idx ON decisions (kind);

INSERT INTO decisions_schema_version (version) VALUES (1) ON CONFLICT (version) DO NOTHING;
