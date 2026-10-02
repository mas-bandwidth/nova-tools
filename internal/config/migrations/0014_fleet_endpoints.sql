-- 0014: the fleet's explicit Redis port and password-free Postgres DSN
-- (internal/config/kind.go: Kinds, "fleet"). Both endpoints stay unset until
-- explicitly declared; neither is inferred from the Redis store machine.
-- The DSN may explicitly name localhost or another host.
ALTER TABLE config.fleet ADD COLUMN IF NOT EXISTS
    redis_port integer CHECK (redis_port BETWEEN 1 AND 65535);
ALTER TABLE config.fleet ADD COLUMN IF NOT EXISTS
    pg_dsn text NOT NULL DEFAULT '';
