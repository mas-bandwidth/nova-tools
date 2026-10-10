-- 0038: the machine's nova root (internal/config/kind.go: Kinds, "machine";
-- docs/SPEC-CONFIG.md, machine): the one root every derived path hangs under
-- (internal/layout), so a machine row moves the bench, secrets, loop logs and
-- mirrors defaults. Nullable: unset (every row before this file, and
-- --nova_root '') is the built-in ~/nova. nova-config refuses a root that is
-- not an existing directory or is a symlink; the column holds no rule of the
-- filesystem's, only the path. The file run again skips the clause.
ALTER TABLE config.machines
    ADD COLUMN IF NOT EXISTS nova_root text;
