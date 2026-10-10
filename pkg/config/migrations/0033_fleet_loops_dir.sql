-- 0033: the fleet row gains a loops_dir field (pkg/config/kind.go:
-- Kinds, "fleet"; pkg/config/redis.go: loopsDir). The directory where
-- loop logs are written is no longer a literal; it is the fleet row's value,
-- seeded to ~/nova-bench/loops so the applied state is byte-identical after
-- apply on a fleet that has not declared one. Carried from the fleet loops
-- card's migration 0027 (dev's 0027_row_name_checks.sql took the slot).
ALTER TABLE config.fleet
    ADD COLUMN IF NOT EXISTS loops_dir text NOT NULL DEFAULT '~/nova-bench/loops';
