-- The tables nova-config reads and writes, in the schema config. For a fresh
-- or an older store: nova-config schema | psql "$NOVA_PG_DSN"
-- Every statement is idempotent, so a store that has them already is left as
-- it is; the ALTERs add the columns an older store lacks and lift two
-- constraints of the old columns, and drop no column.
CREATE SCHEMA IF NOT EXISTS config;

-- One row per write of any row: before and after are the row's fields as
-- JSON text values (after carries "reason" when the write gave one).
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

-- A machine, named by its tailnet host. cores NULL is unknown.
CREATE TABLE IF NOT EXISTS config.machines (
    name       text PRIMARY KEY CONSTRAINT machines_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$'),
    "user"     text NOT NULL,
    runners    integer NOT NULL DEFAULT 0 CHECK (runners >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    note       text NOT NULL DEFAULT '',
    roles      text NOT NULL DEFAULT 'bench'
               CHECK (roles = '' OR roles ~ '^(bench|runner|services)(,(bench|runner|services))*$'),
    address    text NOT NULL DEFAULT '',
    os         text NOT NULL DEFAULT '',
    arch       text NOT NULL DEFAULT '',
    cores      integer CHECK (cores >= 0)
);
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS address text NOT NULL DEFAULT '';
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS os text NOT NULL DEFAULT '';
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS arch text NOT NULL DEFAULT '';
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS cores integer CHECK (cores >= 0);

-- A loop: a command one machine runs, every n seconds or kept alive.
CREATE TABLE IF NOT EXISTS config.loops (
    name       text PRIMARY KEY CONSTRAINT loops_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$'),
    machine    text NOT NULL REFERENCES config.machines (name),
    argv       text NOT NULL CHECK (argv LIKE '[%]'),
    seat       text NOT NULL DEFAULT '',
    keys       text NOT NULL DEFAULT '' CHECK (keys = '' OR seat <> ''),
    every      integer NOT NULL DEFAULT 0 CHECK (every >= 0),
    keepalive  boolean NOT NULL DEFAULT false,
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    env        text NOT NULL DEFAULT '{}' CHECK (env LIKE '{%}'),
    log        text NOT NULL DEFAULT '',
    CHECK ((every > 0) <> keepalive)
);
ALTER TABLE config.loops ADD COLUMN IF NOT EXISTS env text NOT NULL DEFAULT '{}' CHECK (env LIKE '{%}');
ALTER TABLE config.loops ADD COLUMN IF NOT EXISTS log text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS loops_machine ON config.loops (machine, name);

-- Older stores: a fleet table (a kind this tool no longer has; it is left as it
-- is, unread) had store and coordinator pointing at machines, and machines.seat
-- was required. Lift those, so a machine can be removed or added; no column
-- is dropped.
ALTER TABLE IF EXISTS config.fleet DROP CONSTRAINT IF EXISTS fleet_store_fkey;
ALTER TABLE IF EXISTS config.fleet DROP CONSTRAINT IF EXISTS fleet_coordinator_fkey;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = 'config' AND table_name = 'machines' AND column_name = 'seat') THEN
        ALTER TABLE config.machines ALTER COLUMN seat DROP NOT NULL;
    END IF;
END $$;
