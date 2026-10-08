-- 0037: the model kind and a friend's models (internal/config/kind.go: Kinds,
-- "model" and "friend"; docs/SPEC-CONFIG.md, "model"). A model row is one a
-- friend runs in her own harness, with its tier; it is never dealt from, as a
-- route is. A friend row lists her models strongest to weakest (the owner,
-- 2026-10-06: "you need to express this in terms of an array of models this
-- agent can do, strongest to weakest"): her class is the tier of the first,
-- and the tiers the dealer may hand her are the tiers of all of them, derived
-- and never stored. Her tiers column stays as the fallback of a row with no
-- models, and a row that lists models carries none.
CREATE TABLE IF NOT EXISTS config.models (
    name       text PRIMARY KEY CONSTRAINT models_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$'),
    tier       text NOT NULL CHECK (tier IN ('flash', 'pro', 'heavy', 'frontier')),
    note       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS models text NOT NULL DEFAULT ''
        CHECK (models ~ '^([a-z0-9][a-z0-9-]*(,[a-z0-9][a-z0-9-]*)*)?$');

-- No row is seeded: the models of a fleet's friends are its data, set by
-- `nova-config model add` and `nova-config friend set <f> --models`; until
-- then each friend row keeps its tiers fallback, and her class is the highest
-- of them.
