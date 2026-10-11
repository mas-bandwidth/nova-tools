-- 0031: the sprint row's off switch of the tick's rule answers
-- (pkg/config/kind.go: Kinds, "sprint", AnswerRules; docs/SPEC-SPRINT.md
-- section 8, answered by rule): a comma list of rule names, deduplicated and
-- sorted, each a rule the tick does not answer by while it is listed; '' (the
-- default) turns none off. Apply writes it to sprint:answer_rules_off, which the
-- sprint's routes read takes, and the lander reads base-gate there.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS answer_rules_off text NOT NULL DEFAULT '';
