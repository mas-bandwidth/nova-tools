-- 0015: the note of a route and of a machine (internal/config/kind.go: Kinds,
-- "route" and "machine"), free text and one line, the reason a choice was made
-- (the owner, 2026-10-02: "your choices, these should be saved somewhere permanent
-- with notes (ideally, nova-config)"). A disabled route carries its reason: the
-- kind's Check and the verb refuse a disabled route with an empty note, and no
-- CHECK here repeats it, because a route disabled before this file has no note
-- until someone writes it. Every old row gets '' (a backfill from nothing), and
-- the file run again keeps a note written since. Its history rows carry it like
-- every other field (config.history, 0001).
ALTER TABLE config.routes
    ADD COLUMN IF NOT EXISTS note text NOT NULL DEFAULT '';
ALTER TABLE config.machines
    ADD COLUMN IF NOT EXISTS note text NOT NULL DEFAULT '';
