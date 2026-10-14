-- 0045: a tier's routes are derived from route.tier, never stored
-- (pkg/config/kind.go, Kinds, "tier"; docs/SPEC-CONFIG.md, "tier").
-- Membership is the enabled routes whose tier field names them, so the
-- routes column was a second copy of route.tier and it is dropped. apply
-- writes tier:<t>'s routes field from the route rows on every run, so an
-- applied array that named routes in another order, or named a route since
-- disabled or moved, is put right.
ALTER TABLE config.tiers DROP COLUMN IF EXISTS routes;
