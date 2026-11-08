-- The tables nova-config reads and writes, in the schema config: for a fresh
-- store, psql <dsn> -f schema.sql. Every statement is IF NOT EXISTS, so a
-- store that has them already is left as it is.
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

-- A machine, named by its tailnet host. width NULL is unset.
CREATE TABLE IF NOT EXISTS config.machines (
    name       text PRIMARY KEY CONSTRAINT machines_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$'),
    "user"     text NOT NULL,
    seat       text NOT NULL,
    slots      integer NOT NULL DEFAULT 0 CHECK (slots >= 0),
    runners    integer NOT NULL DEFAULT 0 CHECK (runners >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    width      integer CHECK (width >= 0),
    note       text NOT NULL DEFAULT '',
    tla        boolean NOT NULL DEFAULT false,
    roles      text NOT NULL DEFAULT 'bench'
               CHECK (roles = '' OR roles ~ '^(bench|coordination|runner|services)(,(bench|coordination|runner|services))*$'),
    gates_only text NOT NULL DEFAULT ''
);

-- The one fleet row: store and coordinator name machines, or are NULL.
CREATE TABLE IF NOT EXISTS config.fleet (
    name        text PRIMARY KEY CHECK (name = 'fleet'),
    store       text REFERENCES config.machines (name),
    coordinator text REFERENCES config.machines (name),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    redis_port  integer CHECK (redis_port BETWEEN 1 AND 65535),
    pg_dsn      text NOT NULL DEFAULT '',
    bus         text NOT NULL DEFAULT '',
    loops_dir   text NOT NULL DEFAULT '~/nova-bench/loops'
);
INSERT INTO config.fleet (name) VALUES ('fleet') ON CONFLICT (name) DO NOTHING;

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
    CHECK ((every > 0) <> keepalive)
);
CREATE INDEX IF NOT EXISTS loops_machine ON config.loops (machine, name);
