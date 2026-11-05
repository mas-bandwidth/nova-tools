-- 0048: the friend one-shot mode is removed (the owner, 2026-11-05: "Let's remove
-- one shot, lest we switch back to it again for no reason."). A friend runs her
-- own loop: nova-sprint take --as friend.<name>, work, nova-sprint finish --as
-- friend.<name>, with nova-hook as her liveness. mode (0030: how the nova-friend
-- daemon handed her cards), config_dir (0034) and token_cap (0035), which only
-- the daemon's lanes read, and session (0045: a fleet member running her row)
-- have no reader left, so the columns go. The file run again skips each clause.
ALTER TABLE config.friends
    DROP COLUMN IF EXISTS mode,
    DROP COLUMN IF EXISTS config_dir,
    DROP COLUMN IF EXISTS token_cap,
    DROP COLUMN IF EXISTS session;
