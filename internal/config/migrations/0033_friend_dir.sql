-- 0033: the friend's working directory (internal/config/kind.go: Kinds,
-- "friend"; docs/SPEC-CONFIG.md, friend): the absolute path of the directory
-- nova-sprint delivers her cards into and reads her outbox from. Nullable:
-- unset, every row before this file, is <root>/<name>-working as before (the
-- owner, 2026-10-05: "All friends should be updated to point to their real
-- directories. I'd like the symlinks to go away"). nova-config refuses a dir
-- that is not an existing directory or is a symlink; the column holds no rule
-- of the filesystem's, only the path. The file run again skips the clause.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS dir text;
