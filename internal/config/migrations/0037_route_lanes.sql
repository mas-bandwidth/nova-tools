-- 0037: the route's lane cap (internal/config/kind.go: Kinds, "route";
-- docs/SPEC-CONFIG.md, route; docs/SPEC-SPRINT.md, the deal). The most lanes in
-- flight on the route at once: the deal skips it while that many cards or reads
-- run on it and takes the next route of the tier, and a provider rate limit
-- halves it for ten minutes. 0 (the default) is unlimited. The file run again
-- skips the clause.
ALTER TABLE config.routes
    ADD COLUMN IF NOT EXISTS lanes integer NOT NULL DEFAULT 0 CHECK (lanes >= 0);
