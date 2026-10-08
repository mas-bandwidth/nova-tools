-- 0037: the friend's session (internal/config/kind.go: Kinds, "friend";
-- docs/SPEC-FRIEND.md, "A gone session target"): the session her daemon
-- delivers into, as nova-friend rebind or install last recorded it. Her beat
-- answers it (row_session=), and the daemon reads that answer before its
-- startup proof and before any turn. A daemon started on any other --session
-- is target-invalid and delivers nothing, so a service reinstalled from an
-- old command line cannot bring a retired id back. Nullable: a row before
-- this file names none. The file run again skips the clause.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS session text;
