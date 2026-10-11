-- 0021: the sprint row's two bars on a decide read's p(defect)
-- (pkg/config/kind.go: Kinds, "sprint"; pkg/decide, Bars): the first
-- read of a flash card bounces the work at or above decide_bounce, lands it
-- with no model read below decide_review, and sends it to a strings read
-- between the two (docs/SPEC-SPRINT.md section 6, the decide read). Decimals
-- kept as text in their one spelling; both '' turns the decide read off. The
-- defaults are the calibration of 2026-10-02 (234 reviewed cards, AUC 0.869).
-- Apply writes them to sprint:decide_bounce and sprint:decide_review, which
-- the sprint's routes read takes.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS decide_bounce text NOT NULL DEFAULT '0.5' CHECK (decide_bounce ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS decide_review text NOT NULL DEFAULT '0.3' CHECK (decide_review ~ '^([0-9]+(\.[0-9]+)?)?$');
