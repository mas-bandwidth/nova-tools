-- 0033: the friend's working directory (internal/config/kind.go: Kinds, friend;
-- docs/SPEC-CONFIG.md, friend). Nullable: an absolute path to the friend's
-- real working directory on the host machine, never a symlink. Unset until
-- declared, where tools fall back to <root>/<friend>-working.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS dir text;
