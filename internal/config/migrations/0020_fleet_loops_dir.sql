-- 0020: the fleet row gains a loops_dir field (internal/config/kind.go:
-- Kinds, "fleet"; internal/config/redis.go: hashKinds, the loop kind's
-- derived log). The directory where loop logs are written is no longer a
-- literal; it is the fleet row's value, seeded to ~/nova-bench/loops so the
-- applied state is byte-identical after apply on a fleet that has not
-- declared one.
ALTER TABLE config.fleet ADD COLUMN loops_dir text NOT NULL DEFAULT '~/nova-bench/loops';
