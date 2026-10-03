-- 0022: the sprint row's bar on a judgment decision's probability
-- (internal/config/kind.go: Kinds, "sprint"; internal/decide, Choose):
-- nova-sprint answer --decide applies the verb the judgment decision chose
-- when its probability is at or above it, and lists it for the coordinator
-- below it (docs/SPEC-SPRINT.md section 8). A decimal kept as text in its one
-- spelling; '' (the default) applies nothing: every decision is recorded and
-- what a bar would apply is listed, until the coordinator sets one. Apply
-- writes it to sprint:decide_judgment_bar, which the sprint's routes read takes.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS decide_judgment_bar text NOT NULL DEFAULT '' CHECK (decide_judgment_bar ~ '^([0-9]+(\.[0-9]+)?)?$');
