-- 0038: the sprint row's runaway-test threshold (internal/config/kind.go: Kinds,
-- "sprint", FieldTestsAlarm; docs/SPEC-SPRINT.md, runaway test processes): how many
-- live processes whose name ends in .test one member or friend may beat before the
-- tick raises one judgment naming the oldest parent pid. A whole number from 1; 0 (the
-- default) is four times the member's or friend's width. Apply writes it to
-- sprint:tests_alarm, which the sprint's routes read takes.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS tests_alarm integer NOT NULL DEFAULT 0 CHECK (tests_alarm >= 0);
