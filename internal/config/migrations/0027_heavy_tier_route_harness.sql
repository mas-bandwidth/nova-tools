-- 0027: the heavy tier and the route's harness (internal/config/kind.go: Kinds, "route"
-- and "tier"; docs/SPEC-SWARM.md, the headless harnesses). heavy is the class between
-- pro and frontier: the headless subscription harnesses of one machine, run as one-shot
-- children (the owner, 2026-10-04: a class, never a model name; the model stays on the
-- route row). A route names the harness its children run under: opencode (the default,
-- launched through the providers table with the row's provider) or one of the headless
-- programs claude, codex and grok, whose login is the machine's own.
ALTER TABLE config.routes DROP CONSTRAINT IF EXISTS routes_tier_check;
ALTER TABLE config.routes ADD CONSTRAINT routes_tier_check CHECK (tier IN ('flash', 'pro', 'heavy'));
ALTER TABLE config.routes
    ADD COLUMN IF NOT EXISTS harness text NOT NULL DEFAULT 'opencode'
        CHECK (harness IN ('opencode', 'claude', 'codex', 'grok'));

ALTER TABLE config.tiers DROP CONSTRAINT IF EXISTS tiers_name_check;
ALTER TABLE config.tiers ADD CONSTRAINT tiers_name_check CHECK (name IN ('flash', 'pro', 'heavy'));
INSERT INTO config.tiers (name) VALUES ('heavy') ON CONFLICT (name) DO NOTHING;
