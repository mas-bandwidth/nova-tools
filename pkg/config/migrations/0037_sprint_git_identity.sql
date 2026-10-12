-- 0037: the sprint row's author and committer for every commit the sprint
-- machine makes (pkg/config/kind.go: Kinds, "sprint", FieldGitIdentity;
-- docs/SPEC-SPRINT.md, the machine's commit identity): "Name <email>", the
-- default nova-sprint <nova-sprint@mas-bandwidth.com>. Apply writes it to
-- sprint:git_identity, which the lander reads; the landing refuses to commit
-- when it is empty, and doctor fails when its email equals the host's global
-- user.email.
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS git_identity text NOT NULL DEFAULT 'nova-sprint <nova-sprint@mas-bandwidth.com>';
