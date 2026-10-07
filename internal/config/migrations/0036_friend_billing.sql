-- 0036: how a friend's work is paid (internal/config/kind.go: Kinds, friend billing).
-- Existing rows use the default subscription billing; API billing is explicit.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS billing text NOT NULL DEFAULT 'subscription' CHECK (billing IN ('api', 'subscription'));
