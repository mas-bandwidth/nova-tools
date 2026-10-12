-- 0037: the friend's pronouns (pkg/config/kind.go: Kinds, "friend";
-- docs/SPEC-CONFIG.md, friend). One of she/her, he/him or they/them; a row
-- that names none goes by they/them, and no friend is named here: the owner
-- sets a friend's pronouns with nova-config after deploy. The file run again
-- skips the clause.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS pronouns text NOT NULL DEFAULT 'they/them' CHECK (pronouns IN ('she/her', 'he/him', 'they/them'));
