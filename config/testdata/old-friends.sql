-- An older config.friends table, its columns and checks
-- as \d prints them, and config.sprint naming a coordinator; seeded with two
-- rows the way the older tool wrote them. Idempotent, as startPG runs it twice.
CREATE SCHEMA IF NOT EXISTS config;
CREATE TABLE IF NOT EXISTS config.friends (
    name       text PRIMARY KEY CONSTRAINT friends_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$'),
    slots      integer NOT NULL DEFAULT 0 CHECK (slots >= 0),
    tiers      text NOT NULL DEFAULT '',
    roles      text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    width      integer NOT NULL DEFAULT 8 CHECK (width >= 1),
    mode       text NOT NULL DEFAULT 'batch' CHECK (mode = ANY (ARRAY['batch', 'one-shot'])),
    config_dir text,
    token_cap  integer NOT NULL DEFAULT 6000000 CHECK (token_cap >= 0),
    streams    text NOT NULL DEFAULT '',
    kinds      text NOT NULL DEFAULT '',
    pronouns   text CHECK (pronouns = ANY (ARRAY['she/her', 'he/him', 'they/them'])),
    session    text NOT NULL DEFAULT 'live' CHECK (session = ANY (ARRAY['live', 'one-shot']))
);
CREATE TABLE IF NOT EXISTS config.sprint (
    name        text PRIMARY KEY CHECK (name = 'sprint'),
    coordinator text REFERENCES config.friends (name)
);
INSERT INTO config.friends (name, slots, tiers, roles, width, pronouns) VALUES
    ('alex', 8, 'heavy,pro', 'builder', 2, 'he/him'),
    ('coord', 16, 'flash,frontier,heavy,pro', 'builder,reader', 32, 'they/them')
    ON CONFLICT DO NOTHING;
INSERT INTO config.sprint (name, coordinator) VALUES ('sprint', 'coord') ON CONFLICT DO NOTHING;
