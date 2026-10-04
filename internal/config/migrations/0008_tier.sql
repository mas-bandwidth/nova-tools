-- 0008: the tier kind (internal/config/kind.go: Kinds, "tier"), a model
-- tier's route array: routes is the ordered comma list of the tier's route
-- names, a name repeated for more turns ("" takes the tier's enabled routes
-- in name order); the deal takes routes[index mod len] for each card of the
-- tier (internal/sprint/route.go). One row each for flash and pro, created
-- here, so set takes them. The route's weight leaves the route kind: the
-- array is how a route gets more turns. Its history rows are the one
-- config.history table's (0001), kind 'tier'.
CREATE TABLE IF NOT EXISTS config.tiers (
    name       text PRIMARY KEY CHECK (name IN ('flash', 'pro')),
    routes     text NOT NULL DEFAULT '' CHECK (routes ~ '^([a-z0-9][a-z0-9-]*(,[a-z0-9][a-z0-9-]*)*)?$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO config.tiers (name) VALUES ('flash'), ('pro') ON CONFLICT (name) DO NOTHING;

ALTER TABLE config.routes DROP COLUMN IF EXISTS weight;
