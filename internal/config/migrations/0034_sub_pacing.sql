-- 0034: a subscription friend is paced to her plan's reset (internal/config/kind.go:
-- Kinds, "friend" and "sprint"; docs/SPEC-SPRINT.md section 1, sub-pacingb.w1; the
-- owner, 2026-10-04: "always make sure that subs are 100% utilized before spending
-- on fleet", and "if we find we are exhausting the rowan buds too quick for the
-- weekly plan"). The friend row gains windows, her plan's shape (one or two of 5h
-- and weekly, comma separated; empty, the default every row before this file takes,
-- is no pacing: she runs at her width as before), and usage, where her burn is read
-- (limit-messages, the default: the tokens of each card and the limit messages seen;
-- claude-status; codex-limit). The sprint row gains paid_width, the
-- subscription-first switch: false (the default) deals no card a subscription friend
-- covers to the fleet's paid routes. The file run again skips every clause, so it
-- never overwrites a value set since.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS windows text NOT NULL DEFAULT '';
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS usage text NOT NULL DEFAULT 'limit-messages' CHECK (usage IN ('limit-messages', 'claude-status', 'codex-limit'));
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS paid_width boolean NOT NULL DEFAULT false;
