-- 0032: the friend's restriction on the work she is dealt (internal/config/kind.go:
-- Kinds, "friend"; docs/SPEC-CONFIG.md): streams, a comma list of glob patterns over
-- stream names, and kinds, a comma list of card KIND values; empty is no restriction,
-- what every friend had before this file, so every row there before it takes ''. The
-- file run again skips the whole clause, so it never overwrites a restriction set since.
-- A row's restriction is set with nova-config friend set <name> --streams <globs>.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS streams text NOT NULL DEFAULT '';
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS kinds text NOT NULL DEFAULT '';
