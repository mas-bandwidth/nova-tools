-- 0022: the sprint row's bars of nova-decide's layer 2 (internal/config/kind.go:
-- Kinds, "sprint"; internal/decide, attempt.go and grade.go): a failed take whose
-- attempt class is given at or above decide_attempt is routed by the class, not by
-- its reason line's prefix (docs/SPEC-SPRINT.md section 2), and a card graded pro at
-- or above decide_grade starts on pro instead of flash (section 5). Decimals kept as
-- text in their one spelling; '' is no bar, and both are '' by default: the
-- decisions are asked, recorded and shown, and route nothing until a review round
-- labels cards independently (0.7 is the calibration's starting point for each).
-- Apply writes them to sprint:decide_attempt and sprint:decide_grade, which the
-- sprint's routes read takes.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS decide_attempt text NOT NULL DEFAULT '' CHECK (decide_attempt ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS decide_grade text NOT NULL DEFAULT '' CHECK (decide_grade ~ '^([0-9]+(\.[0-9]+)?)?$');
