-- 0028: the route's first field (internal/config/kind.go: Kinds, "route";
-- docs/SPEC-CONFIG.md, route). True deals that route before the others of its
-- tier. Old rows backfill to false, which is the walk the deal already did.
ALTER TABLE config.routes
    ADD COLUMN IF NOT EXISTS first boolean NOT NULL DEFAULT false;
