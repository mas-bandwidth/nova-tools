-- 0015: the fleet's loops directory (internal/config/kind.go: Kinds, "fleet"),
-- seeded so applied state is byte-identical to the former hard-coded path.
ALTER TABLE config.fleet ADD COLUMN IF NOT EXISTS
    loops_dir text NOT NULL DEFAULT '~/nova-bench/loops';
UPDATE config.fleet SET loops_dir = '~/nova-bench/loops'
    WHERE loops_dir IS NULL OR loops_dir = '';
