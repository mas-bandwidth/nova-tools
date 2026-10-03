-- 0020: the fleet row's loops_dir (internal/config/kind.go: Kinds, "fleet").
-- A loop's log is that directory and the loop's name. The existing row is
-- seeded so apply writes the same log values as before this column.
ALTER TABLE config.fleet ADD COLUMN IF NOT EXISTS
    loops_dir text NOT NULL DEFAULT '';
UPDATE config.fleet SET loops_dir = '~/nova-bench/loops' WHERE name = 'fleet' AND loops_dir = '';
