-- The tables nova-config reads and writes, in the schema config:
-- nova-config schema | PGPASSWORD="$NOVA_PG_PASSWORD" psql "$NOVA_PG_DSN"
-- Every statement is idempotent, so a store that has them already is left as
-- it is.
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
-- the internet. runner_scopes is its Linux and macOS GitHub Actions runners and
-- windows_runners a WSL machine's Windows-side runners, each
-- <url>=<count>[:<label>+<label>] comma-separated ('' is none).
-- windows_admin_runners is a WSL machine's Windows-side runners run as a local
-- administrator account, one per <url>=<account>:<label>[+<label>]@<workflow>,
-- comma-separated ('' is none). runner_env is the environment its runners'
-- services start with, a JSON object of NAME to value ('{}' is none).
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
    "public"   text,
    runner_scopes text NOT NULL DEFAULT '',
    windows_runners text NOT NULL DEFAULT '',
    windows_admin_runners text NOT NULL DEFAULT '',
    runner_env text NOT NULL DEFAULT '{}' CHECK (runner_env LIKE '{%}'),
    provider   text NOT NULL DEFAULT 'owned' CHECK (provider IN ('owned', 'latitude', 'aws')),
    provider_id text NOT NULL DEFAULT '',
    market     text NOT NULL DEFAULT '' CHECK (market IN ('', 'on-demand', 'spot')),
    spot_request text NOT NULL DEFAULT '',
    price      text NOT NULL DEFAULT '' CHECK (price ~ '^([0-9]{1,6}(\.[0-9]{1,4})?)?$'),
    price_from text NOT NULL DEFAULT '' CHECK (price_from IN ('', 'provider', 'recalled')),
    purpose    text NOT NULL DEFAULT '',
    rented_by  text NOT NULL DEFAULT '',
    rented_at  text NOT NULL DEFAULT '' CHECK (rented_at ~ '^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)?$'),
    return_by  text NOT NULL DEFAULT '' CHECK (return_by ~ '^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)?$')
);
-- A machines table made before the runner_env column gets it here; on one that has it this does nothing.
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS runner_env text NOT NULL DEFAULT '{}' CHECK (runner_env LIKE '{%}');
-- A machines table made before the runner_scopes column gets it here ('': none, as every machine had); on one that has it this does nothing.
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS runner_scopes text NOT NULL DEFAULT '';
-- A machines table made before the windows_admin_runners column gets it here ('': none); on one that has it this does nothing.
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS windows_admin_runners text NOT NULL DEFAULT '';
-- A machine rented by the hour (nova-fleet rent): provider is owned or the provider it is rented from; provider_id
-- its id there; market on-demand or spot (aws), spot_request the spot request; price US dollars an hour and
-- price_from provider (read from the provider) or recalled (not read); purpose what it is for, rented_by who rented
-- it, rented_at and return_by RFC 3339 UTC times; '' is unset. A machines table made before these columns gets them
-- here, every row owned; on one that has them this does nothing.
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS provider text NOT NULL DEFAULT 'owned' CHECK (provider IN ('owned', 'latitude', 'aws'));
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS provider_id text NOT NULL DEFAULT '';
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS market text NOT NULL DEFAULT '' CHECK (market IN ('', 'on-demand', 'spot'));
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS spot_request text NOT NULL DEFAULT '';
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS price text NOT NULL DEFAULT '' CHECK (price ~ '^([0-9]{1,6}(\.[0-9]{1,4})?)?$');
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS price_from text NOT NULL DEFAULT '' CHECK (price_from IN ('', 'provider', 'recalled'));
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS purpose text NOT NULL DEFAULT '';
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS rented_by text NOT NULL DEFAULT '';
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS rented_at text NOT NULL DEFAULT '' CHECK (rented_at ~ '^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)?$');
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS return_by text NOT NULL DEFAULT '' CHECK (return_by ~ '^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)?$');

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
CREATE INDEX IF NOT EXISTS services_machine ON config.services (machine, name);

-- A friend: an AI that works on the fleet, named by its bus name; width is
-- the cards it works at once, tiers a comma list of flash, frontier, heavy, pro.
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
