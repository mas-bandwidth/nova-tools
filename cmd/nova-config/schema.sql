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
    runner_env text NOT NULL DEFAULT '{}' CHECK (runner_env LIKE '{%}')
);
-- A machines table made before the runner_env column gets it here; on one that has it this does nothing.
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS runner_env text NOT NULL DEFAULT '{}' CHECK (runner_env LIKE '{%}');
-- A machines table made before the runner_scopes column gets it here ('': none, as every machine had); on one that has it this does nothing.
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS runner_scopes text NOT NULL DEFAULT '';
-- A machines table made before the windows_admin_runners column gets it here ('': none); on one that has it this does nothing.
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS windows_admin_runners text NOT NULL DEFAULT '';

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

-- A box rented by the hour (nova-fleet rent), in a table of its own: a nova-config that predates it never reads it,
-- so a box no play may reach (one at aws, or one not created yet) is in no inventory, whichever release builds it.
-- provider is where it is rented and provider_id its id there; market on-demand or spot (aws) and spot_request its
-- spot request; address and user what ssh reaches it at; price US dollars an hour and price_from provider (read
-- from the provider) or recalled (not read); purpose what it is for, rented_by who rented it, rented_at and return_by
-- RFC 3339 UTC times; '' is unset. A latitude box that joins the fleet also has a machine row, as owned ones do.
CREATE TABLE IF NOT EXISTS config.rented (
    name       text PRIMARY KEY CONSTRAINT rented_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$'),
    provider   text NOT NULL CHECK (provider IN ('latitude', 'aws')),
    provider_id text NOT NULL DEFAULT '',
    market     text NOT NULL DEFAULT '' CHECK (market IN ('', 'on-demand', 'spot')),
    spot_request text NOT NULL DEFAULT '',
    address    text NOT NULL DEFAULT '',
    "user"     text NOT NULL DEFAULT '',
    price      text NOT NULL DEFAULT '' CHECK (price ~ '^([0-9]{1,6}(\.[0-9]{1,4})?)?$'),
    price_from text NOT NULL DEFAULT '' CHECK (price_from IN ('', 'provider', 'recalled')),
    purpose    text NOT NULL CHECK (purpose <> ''),
    rented_by  text NOT NULL CHECK (rented_by <> ''),
    rented_at  text NOT NULL CHECK (rented_at ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$'),
    return_by  text NOT NULL DEFAULT '' CHECK (return_by ~ '^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)?$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT rented_market CHECK ((provider = 'aws') = (market <> '')),
    CONSTRAINT rented_spot CHECK (spot_request = '' OR market = 'spot'),
    CONSTRAINT rented_return CHECK (return_by = '' OR return_by >= rented_at)
);

-- The store holds these whoever writes, a release before config.rented included:
-- - a rented record that still holds a box id or a spot request id is not deleted (either may still bill; clear it
--   once it is gone at the provider);
-- - while it holds either, what the handle names is fixed: name, provider and market do not change, and a handle is
--   only given (from '') or cleared (to ''), never replaced;
-- - no machine row shares a name with an aws rented box (a machine row is what an inventory, and so a play, reaches).
--   Both checks take a transaction lock on the name first, so two writers at once, one to each table, are serialized:
--   the second reads the first's committed row (each statement of a function reads afresh at READ COMMITTED).
CREATE OR REPLACE FUNCTION config.rented_keep() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.provider_id <> '' OR OLD.spot_request <> '' THEN
        RAISE EXCEPTION 'rented % still has a box or spot request at %, which may bill; return it, then clear them; run: nova-fleet rented', OLD.name, OLD.provider;
    END IF;
    RETURN OLD;
END $$;
CREATE OR REPLACE TRIGGER rented_keep BEFORE DELETE ON config.rented FOR EACH ROW EXECUTE FUNCTION config.rented_keep();
CREATE OR REPLACE FUNCTION config.rented_fixed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (OLD.provider_id <> '' OR OLD.spot_request <> '') AND (NEW.name <> OLD.name OR NEW.provider <> OLD.provider
        OR NEW.market <> OLD.market OR (OLD.provider_id <> '' AND NEW.provider_id NOT IN (OLD.provider_id, ''))
        OR (OLD.spot_request <> '' AND NEW.spot_request NOT IN (OLD.spot_request, ''))) THEN
        RAISE EXCEPTION 'rented % holds a box or spot request at %, which may bill: its name, provider, market and handles stay as they are until each handle is cleared; run: nova-fleet rented', OLD.name, OLD.provider;
    END IF;
    RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER rented_fixed BEFORE UPDATE ON config.rented FOR EACH ROW EXECUTE FUNCTION config.rented_fixed();
CREATE OR REPLACE FUNCTION config.machines_not_aws() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('config.name:' || NEW.name));
    IF EXISTS (SELECT 1 FROM config.rented WHERE name = NEW.name AND provider = 'aws') THEN
        RAISE EXCEPTION 'machine %: % is a rented aws box, which no play may reach, so it has no machine row', NEW.name, NEW.name;
    END IF;
    RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER machines_not_aws BEFORE INSERT OR UPDATE OF name ON config.machines FOR EACH ROW EXECUTE FUNCTION config.machines_not_aws();
CREATE OR REPLACE FUNCTION config.rented_not_machine() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('config.name:' || NEW.name));
    IF NEW.provider = 'aws' AND EXISTS (SELECT 1 FROM config.machines WHERE name = NEW.name) THEN
        RAISE EXCEPTION 'rented %: an aws box, which no play may reach, cannot share its name with machine %', NEW.name, NEW.name;
    END IF;
    RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER rented_not_machine BEFORE INSERT OR UPDATE OF name, provider ON config.rented FOR EACH ROW EXECUTE FUNCTION config.rented_not_machine();
