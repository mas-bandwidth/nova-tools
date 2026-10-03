-- 0022: the sprint row's bar on a landed diff's score (internal/config/kind.go:
-- Kinds, "sprint"; internal/decide, Top): land scores every landed diff with
-- nova-decide's score decision, and a batch whose cards' top class has a p at or
-- above decide_score_bar raises one "landed work scored low" judgment listing
-- them (docs/SPEC-SPRINT.md section 7, the landed score). A decimal kept as text
-- in its one spelling; '' raises none. Apply writes it to sprint:decide_score_bar,
-- which the sprint's routes read takes.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS decide_score_bar text NOT NULL DEFAULT '0.5' CHECK (decide_score_bar ~ '^([0-9]+(\.[0-9]+)?)?$');
