-- 0007: the route kind (pkg/config/kind.go: Kinds, "route"), one way to
-- run a model tier. tier is flash or pro (frontier cards are never drawn
-- from routes, they escalate to the coordinator); provider is the provider
-- word of the model id <provider>/<model>, one word with no slash or blank;
-- model is the name after it, which may hold slashes and holds no blank;
-- tokens is the budget per card, 0 unmetered; deadline is the seconds per
-- card, above 0; weight is the route's weight in the tier's draw, 0 out of
-- it; enabled false takes it out of the draw (the CHECKs below, and the
-- kind's Check before them). Its history rows are the one config.history
-- table's (0001), kind 'route'.
CREATE TABLE IF NOT EXISTS config.routes (
    name       text PRIMARY KEY,
    tier       text NOT NULL CHECK (tier IN ('flash', 'pro')),
    provider   text NOT NULL CHECK (provider ~ '^[^/[:space:]]+$'),
    model      text NOT NULL CHECK (model ~ '^[^[:space:]]+$'),
    tokens     integer NOT NULL DEFAULT 0 CHECK (tokens >= 0),
    deadline   integer NOT NULL CHECK (deadline > 0),
    weight     integer NOT NULL DEFAULT 1 CHECK (weight >= 0),
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS routes_tier ON config.routes (tier, name);
