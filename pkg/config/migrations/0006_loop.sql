-- 0006: the loop kind (pkg/config/kind.go: Kinds, "loop"), a supervised
-- process on one machine. machine names a machine row (a foreign key: a
-- machine a loop runs on cannot be removed); argv is the command as compact
-- JSON, the program first; seat is the nova-secrets seat it opens secrets
-- from and keys the comma list of the secrets' NAMES, never a value; every is
-- the seconds between runs of a periodic loop and keepalive marks a
-- long-running one, exactly one of the two (the CHECK below, and the kind's
-- Check before it); width is a member loop's child cap, 0 for any other;
-- enabled false writes the unit and does not start it. The log path is
-- derived from the name and is not a column. Its history rows are the one
-- config.history table's (0001), kind 'loop'.
CREATE TABLE IF NOT EXISTS config.loops (
    name       text PRIMARY KEY,
    machine    text NOT NULL REFERENCES config.machines (name),
    argv       text NOT NULL CHECK (argv LIKE '[%]'),
    seat       text NOT NULL DEFAULT '',
    keys       text NOT NULL DEFAULT '' CHECK (keys = '' OR seat <> ''),
    every      integer NOT NULL DEFAULT 0 CHECK (every >= 0),
    keepalive  boolean NOT NULL DEFAULT false,
    width      integer NOT NULL DEFAULT 0 CHECK (width >= 0),
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((every > 0) <> keepalive)
);

CREATE INDEX IF NOT EXISTS loops_machine ON config.loops (machine, name);
