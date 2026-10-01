-- 0011: the fleet's explicit Redis port and password-free Postgres DSN
-- (internal/config/kind.go: Kinds, "fleet"). Redis keeps its compatible 6379
-- default. The DSN stays empty until explicitly set: it may name localhost or
-- another host and is never inferred from the Redis store machine.
ALTER TABLE config.fleet ADD COLUMN IF NOT EXISTS
    redis_port integer NOT NULL DEFAULT 6379 CHECK (redis_port BETWEEN 1 AND 65535);
ALTER TABLE config.fleet ADD COLUMN IF NOT EXISTS
    pg_dsn text NOT NULL DEFAULT '';
