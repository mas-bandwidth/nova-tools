-- 0033: the sprint's policy numbers (internal/config/policy.go: SprintPolicies;
-- docs/SPEC-CONFIG.md, "The sprint's policy numbers"): one text column each, '' (the
-- default) being the sprint's compiled default. Apply writes each to sprint:<name>,
-- which the sprint's routes read takes into every tick.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS deal_ahead text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS max_redeals text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS max_read_reasks text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS read_lease text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS readers_window text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS friend_read_deadline text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS friend_idle text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS friend_stall_after text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS friend_stall_step text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS member_down_after text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS loop_silence text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS overload_timeouts text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS overload_window text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS promote_cards text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS promote_age text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS remind_every text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS balance_poll_every text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS flash_gate_bound text NOT NULL DEFAULT '';
