-- 0034: a friend's model per tier, and what her harness can do (internal/config/kind.go:
-- Kinds, "friend"; internal/config/friendmodel.go; docs/SPEC-FRIEND.md, a friend's models).
-- The owner, 2026-10-05: "how do friends know which of THEIR models should be used
-- per-tier?" and "Should this be made part of the friend configuration?" model is a comma
-- list of <tier>=<model>; every row there before this file takes the empty map, so every
-- tier it served is served still, warned until the coordinator fills it. children and
-- child_model say whether her harness runs child agents and whether a child's model can be
-- chosen ("Not all friends can do child agents."); every row there before takes yes and
-- yes, what the deal assumed of her until now. The file run again skips each clause, so it
-- never overwrites a value set since.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS model text NOT NULL DEFAULT '';
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS children text NOT NULL DEFAULT 'yes' CHECK (children IN ('yes', 'no'));
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS child_model text NOT NULL DEFAULT 'yes' CHECK (child_model IN ('yes', 'no'));
