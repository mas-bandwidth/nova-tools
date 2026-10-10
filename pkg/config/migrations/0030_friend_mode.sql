-- 0030: the friend's delivery mode (pkg/config/kind.go: Kinds, "friend";
-- docs/SPEC-FRIEND.md, one-shot lanes): how her daemon hands her work. batch,
-- every waiting message as one turn of her one session, is what every daemon
-- did before this file, so it is the default and every row there before it
-- takes it; one-shot runs width lanes, each its own session of her, each
-- handed one card per turn (the owner, 2026-10-04: "one-shot friends are
-- configured via nova-config", and "so [she] can still be wide, it's just 8
-- [of her]"). The file run again skips the whole clause, so it never
-- overwrites a mode set since.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS mode text NOT NULL DEFAULT 'batch' CHECK (mode IN ('batch', 'one-shot'));
