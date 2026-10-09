-- Public account/profile/seat bindings on one canonical machine. The existing
-- seat column remains the primary seat for older machine rows and tooling.
-- This column contains identifiers only, never a credential value.
ALTER TABLE config.machines
    ADD COLUMN IF NOT EXISTS credential_seats text NOT NULL DEFAULT '';
