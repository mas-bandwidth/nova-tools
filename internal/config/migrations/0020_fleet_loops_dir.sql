-- 0020: the fleet's loops directory (internal/config/kind.go: Kinds, "fleet").
-- The descriptor declares no default. This file seeds the existing fleet
-- row with the directory LoopLog used to bake in, so an apply of that row
-- writes the same log paths as before.
ALTER TABLE config.fleet ADD COLUMN IF NOT EXISTS
    loops_dir text NOT NULL DEFAULT '';
UPDATE config.fleet SET loops_dir = '~/nova-bench/loops'
 WHERE name = 'fleet' AND loops_dir = '';
