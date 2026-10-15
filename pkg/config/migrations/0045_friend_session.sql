-- 0045: whether a friend has a live session (pkg/config/kind.go: Kinds,
-- "friend", session; docs/SPEC-CONFIG.md, friend). live, the default, is a
-- friend with a session between cards, on the psychic link; one-shot is a
-- friend with none, who runs as an ordinary fleet member lane with an identity
-- prompt (nova-swarm member --as friend.<name> --prompt <file>) and is never
-- on the link. Every row stored before reads live, so no friend changes until
-- one is set. The file run again skips the clause.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS session text NOT NULL DEFAULT 'live' CHECK (session IN ('live', 'one-shot'));
