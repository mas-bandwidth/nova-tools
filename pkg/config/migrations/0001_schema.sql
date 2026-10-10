-- 0001: the schema, the migration ledger and the one history table
-- (docs/SPEC-CONFIG.md). Every write of every kind is one row here, never an
-- overwrite without a record. A kind's revision is the greatest id of its
-- rows, and apply stamps that into Redis (config:decl).
CREATE SCHEMA IF NOT EXISTS config;

CREATE TABLE IF NOT EXISTS config.schema_migrations (
    version    integer PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS config.history (
    id     bigserial PRIMARY KEY,
    kind   text NOT NULL,
    name   text NOT NULL,
    op     text NOT NULL CHECK (op IN ('add', 'set', 'remove')),
    before jsonb,
    after  jsonb,
    actor  text NOT NULL,
    at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS history_kind_name ON config.history (kind, name, id);
