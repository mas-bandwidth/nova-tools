-- 0032: the route's first field (pkg/config/kind.go: Kinds, "route";
-- docs/SPEC-CONFIG.md, route). True deals that route before the others of its
-- tier. A row with no value is false, the walk the deal does when no route
-- of the tier has first set.
ALTER TABLE config.routes
    ADD COLUMN IF NOT EXISTS first boolean NOT NULL DEFAULT false;
