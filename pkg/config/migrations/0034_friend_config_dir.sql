-- 0034: the friend's config_dir (pkg/config/kind.go: Kinds, "friend";
-- docs/SPEC-FRIEND.md, one-shot lanes): the directory a claude one-shot lane
-- runs with as CLAUDE_CONFIG_DIR, so each friend row is its own account's
-- login and settings. Nullable: a row before this file, and any friend whose
-- harness is not claude, has none; nova-friend run refuses a claude row in
-- one-shot mode without one. The file run again skips the clause.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS config_dir text;
