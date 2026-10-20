-- 0049: a machine's declared tags (pkg/config/kind.go: Kinds, "machine", tags;
-- docs/SPEC-CONFIG.md, "The tag registry"; tla/Tags.tla). The tags field is the
-- tags the machine row declares; the member proves the registry's probed tags on
-- its heartbeat. It is Postgres-only: the sprint reads it live and Redis never
-- holds a copy. gates_only is read as the machine's exclusive tags and is
-- unchanged here.
ALTER TABLE config.machines
    ADD COLUMN IF NOT EXISTS tags text NOT NULL DEFAULT '';
