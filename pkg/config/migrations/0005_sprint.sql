-- 0005: the sprint kind (pkg/config/kind.go: Kinds, "sprint"), the one
-- row of sprint-global facts: the coordinator is sprint-global configuration.
-- The migration creates the row; set changes it. coordinator names a friend
-- row (a foreign key: a friend the sprint names cannot be removed) or is
-- NULL for none.
CREATE TABLE IF NOT EXISTS config.sprint (
    name        text PRIMARY KEY CHECK (name = 'sprint'),
    coordinator text REFERENCES config.friends (name),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
INSERT INTO config.sprint (name) VALUES ('sprint') ON CONFLICT (name) DO NOTHING;
