-- 0036: the optional work restriction on a friend row (pkg/config/kind.go, Kinds,
-- "friend"; docs/SPEC-CONFIG.md, friend). Both columns default to empty, which is no
-- restriction, and no friend is named here: the coordinator sets a friend's streams or
-- kinds with nova-config after deploy.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS streams text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS kinds text NOT NULL DEFAULT '';
