-- 0022: the sprint row's bar on a brief decision's p(converges)
-- (internal/config/kind.go: Kinds, "sprint"; internal/decide, BriefBar):
-- nova-sprint add asks the brief decision of each card and refuses a card
-- under the bar, naming the questions it failed (docs/SPEC-NOVA-DECIDE.md
-- section 9). A decimal kept as text in its one spelling; '' (the default)
-- asks and reports only.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS decide_brief_bar text NOT NULL DEFAULT '' CHECK (decide_brief_bar ~ '^([0-9]+(\.[0-9]+)?)?$');
