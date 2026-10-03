-- 0024: the sprint row's two bars on a failed gate's decisions
-- (internal/config/kind.go: Kinds, "sprint"; internal/decide, GateBars): a failing
-- test of a red gate whose p(flaky) is at or above decide_gate_flaky is rerun once
-- before the take (or the lander's batch) is reported red; a work card's failing
-- test whose p(pre-existing) is at or above decide_gate_preexisting is reported
-- `pre-existing: <test>`, never the card's failure (docs/SPEC-SPRINT.md section 5,
-- the gate verdict). Decimals kept as text in their one spelling; '' (the default)
-- takes no route: every gate decision is recorded and shown, and nothing is rerun
-- or reclassified until the owner sets a bar (0.8 is the starting point the
-- calibration of 2026-10-03 reads). Apply writes them to sprint:decide_gate_flaky
-- and sprint:decide_gate_preexisting, which the sprint's routes read takes.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS decide_gate_flaky text NOT NULL DEFAULT '' CHECK (decide_gate_flaky ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS decide_gate_preexisting text NOT NULL DEFAULT '' CHECK (decide_gate_preexisting ~ '^([0-9]+(\.[0-9]+)?)?$');
