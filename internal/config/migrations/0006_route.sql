-- 0006: the route kind (internal/config/kind.go: Kinds, "route") and machine tiers.
-- A model route: provider, model, seat, tier.
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS tiers text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS config.routes (
    name       text PRIMARY KEY,
    provider   text NOT NULL,
    model      text NOT NULL,
    seat       text NOT NULL,
    tier       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
