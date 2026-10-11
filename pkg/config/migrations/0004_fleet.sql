-- 0004: the fleet kind (pkg/config/kind.go: Kinds, "fleet"), the one
-- row of fleet-wide facts: the fleet has a single coordinator at a time.
-- The migration creates the row, so the tool never adds or removes it;
-- set changes it. store and coordinator name machine rows (a foreign key:
-- a machine the fleet names cannot be removed) or are NULL for none.
CREATE TABLE IF NOT EXISTS config.fleet (
    name        text PRIMARY KEY CHECK (name = 'fleet'),
    store       text REFERENCES config.machines (name),
    coordinator text REFERENCES config.machines (name),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
INSERT INTO config.fleet (name) VALUES ('fleet') ON CONFLICT (name) DO NOTHING;
