-- 0035: the sprint row's read tier of each kind of change (internal/config/kind.go:
-- Kinds, "sprint", ReadTierWords; docs/SPEC-SPRINT.md section 6, the read tier by
-- kind): a change whose PATHS are Markdown and text only is read at read_tier_prose,
-- one under tla/ at read_tier_tla, every other at read_tier_code; each one of card,
-- flash, pro, heavy, frontier, card naming the card's own tier. Apply writes each to
-- sprint:<field>, which the sprint's routes read takes.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS read_tier_prose text NOT NULL DEFAULT 'pro',
    ADD COLUMN IF NOT EXISTS read_tier_code text NOT NULL DEFAULT 'card',
    ADD COLUMN IF NOT EXISTS read_tier_tla text NOT NULL DEFAULT 'frontier';
