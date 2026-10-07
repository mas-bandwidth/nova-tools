-- 0036: the friend's release (internal/config/kind.go: Kinds, "friend";
-- docs/SPEC-FRIEND.md, "The row is followed"): the nova-tools release tag
-- her daemon follows, vX.Y.Z. Her beat answers it as row_release=, and a
-- daemon whose own build is another fetches that release's nova-friend,
-- verifies it and restarts itself under it (the owner, 2026-10-07: "nova-config
-- is the thing the runners AUTOMATICALLY FOLLOW"). Nullable: a row before this
-- file, and a friend whose daemon is left as installed, names none, and her
-- daemon keeps the build it runs. The file run again skips the clause.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS release text;
