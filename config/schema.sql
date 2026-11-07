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

-- A machine, named by its tailnet host. cores NULL is unknown. address is the
-- tailnet address the fleet uses; public, NULL when unset, is the address the
-- internet reaches it at (an IP literal or a DNS name), for services that face
-- the internet.
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
    cores      integer CHECK (cores >= 0),
    "public"   text
);
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS address text NOT NULL DEFAULT '';
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS os text NOT NULL DEFAULT '';
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS arch text NOT NULL DEFAULT '';
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS cores integer CHECK (cores >= 0);
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS "public" text;

-- A store older than the rename has this table as config.loops: rename it (and
-- its index and name check) once; a fresh store, or one run twice, skips this.
DO $$
DECLARE
    c record;
BEGIN
    IF to_regclass('config.loops') IS NOT NULL AND to_regclass('config.services') IS NULL THEN
        ALTER TABLE config.loops RENAME TO services;
        ALTER INDEX IF EXISTS config.loops_machine RENAME TO services_machine;
        -- every constraint the table carried under loops_*: the primary key, the name
        -- pattern, the argv, keys, every and either-or checks, the machine foreign key
        -- and the not-null set, so no constraint error says "loops"
        FOR c IN SELECT conname FROM pg_constraint
                 WHERE conrelid = 'config.services'::regclass AND conname LIKE 'loops\_%' ORDER BY conname LOOP
            EXECUTE format('ALTER TABLE config.services RENAME CONSTRAINT %I TO %I', c.conname, 'services_' || substr(c.conname, 7));
        END LOOP;
    END IF;
END $$;

-- A service: a program one machine keeps running; with every n seconds it runs
-- every n seconds instead of staying up.
CREATE TABLE IF NOT EXISTS config.services (
    name       text PRIMARY KEY CONSTRAINT services_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$'),
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
ALTER TABLE config.services ADD COLUMN IF NOT EXISTS env text NOT NULL DEFAULT '{}' CHECK (env LIKE '{%}');
ALTER TABLE config.services ADD COLUMN IF NOT EXISTS log text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS services_machine ON config.services (machine, name);

-- A friend: an AI that works on the fleet, named by its bus name; width is
-- the cards it works at once, tiers a comma list of flash, frontier, heavy, pro.
-- A store that has an older table (slots, roles, mode, config_dir, token_cap,
-- streams, kinds, session, a width default of 8) keeps it and those columns as
-- they are, unread; the ALTERs add the columns it lacks.
CREATE TABLE IF NOT EXISTS config.friends (
    name       text PRIMARY KEY CONSTRAINT friends_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$'),
    harness    text NOT NULL DEFAULT '',
    model      text NOT NULL DEFAULT '',
    width      integer NOT NULL DEFAULT 1 CHECK (width >= 1),
    tiers      text NOT NULL DEFAULT 'flash,frontier,heavy,pro'
               CHECK (tiers = '' OR tiers ~ '^(flash|pro|heavy|frontier)(,(flash|pro|heavy|frontier))*$'),
    pronouns   text,
    note       text NOT NULL DEFAULT '',
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE config.friends ADD COLUMN IF NOT EXISTS harness text NOT NULL DEFAULT '';
ALTER TABLE config.friends ADD COLUMN IF NOT EXISTS model text NOT NULL DEFAULT '';
ALTER TABLE config.friends ADD COLUMN IF NOT EXISTS note text NOT NULL DEFAULT '';
ALTER TABLE config.friends ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true;

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
