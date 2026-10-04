-- 0027: the fleet row's bus store address (internal/config/kind.go: Kinds,
-- "fleet"; docs/SPEC-BUS.md, the config): host:port of the Redis nova-bus
-- talks to, applied as fleet:bus, what the tool reads when NOVA_BUS_REDIS is
-- unset so no friend types the address. Empty until set.
ALTER TABLE config.fleet ADD COLUMN IF NOT EXISTS
    bus text NOT NULL DEFAULT '';
