-- 0034: optional limits on the work a friend may receive (internal/config/kind.go: Kinds, "friend"; docs/SPEC-CONFIG.md, friend restrictions). Empty values preserve the unrestricted behavior.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS streams text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS kinds text NOT NULL DEFAULT '';
