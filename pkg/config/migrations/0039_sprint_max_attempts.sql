-- 0039: the sprint row's attempt bound (pkg/config/kind.go: Kinds, "sprint",
-- FieldMaxAttempts; nova-sprint docs/SPEC-SPRINT.md, the bounds): the attempts one
-- card runs before the sprint raises one judgment to the coordinator, 4 by default,
-- a whole number from 1 to 100. Apply writes it to sprint:max_attempts. The same file
-- in nova-sprint's and nova-tools' pkg/config: the two copies share one migration
-- sequence (0037 sprint git_identity, 0038 friend pronouns, 0039 this). The file run
-- again skips the clause.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS max_attempts integer NOT NULL DEFAULT 4 CHECK (max_attempts BETWEEN 1 AND 100);
