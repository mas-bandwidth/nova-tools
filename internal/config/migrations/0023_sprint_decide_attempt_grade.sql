-- 0023: the sprint row's bars of nova-decide's layer 2 (internal/config/kind.go:
-- Kinds, "sprint"; internal/decide, attempt.go and grade.go). Two classes of the
-- attempt decision may route a failed finish, each from its own bar: a take
-- classed no-result at or above decide_attempt_no_result ends as a take with no
-- result (redealt), and one classed nothing-to-do at or above
-- decide_attempt_nothing_to_do is failed work of that class (docs/SPEC-SPRINT.md
-- section 2); no other class has a bar. A card graded pro at or above
-- decide_grade starts on pro instead of flash (section 5). Decimals kept as text
-- in their one spelling; '' is no bar, and all three are '' by default: the
-- decisions are asked, recorded and shown, and route nothing until a review round
-- labels cards independently (0.7 is the calibration's starting point). Apply
-- writes them to sprint:decide_attempt_no_result,
-- sprint:decide_attempt_nothing_to_do and sprint:decide_grade, which the sprint's
-- routes read takes, each by its name.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS decide_attempt_no_result text NOT NULL DEFAULT '' CHECK (decide_attempt_no_result ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS decide_attempt_nothing_to_do text NOT NULL DEFAULT '' CHECK (decide_attempt_nothing_to_do ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS decide_grade text NOT NULL DEFAULT '' CHECK (decide_grade ~ '^([0-9]+(\.[0-9]+)?)?$');
