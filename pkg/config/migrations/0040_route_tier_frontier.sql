-- 0040: a route may serve frontier (docs/SPEC-CONFIG.md, route).
-- The tier kind keeps its three seeded rows; only the route tier check widens.
ALTER TABLE config.routes DROP CONSTRAINT IF EXISTS routes_tier_check;
ALTER TABLE config.routes ADD CONSTRAINT routes_tier_check
    CHECK (tier IN ('flash', 'frontier', 'heavy', 'pro'));
