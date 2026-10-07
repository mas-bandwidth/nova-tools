-- 0011: reader_tier leaves the sprint row (internal/config/kind.go: Kinds,
-- "sprint"): a read runs on its card's tier (internal/sprint), so nothing
-- reads the column 0010 added.
ALTER TABLE config.sprint DROP COLUMN IF EXISTS reader_tier;
