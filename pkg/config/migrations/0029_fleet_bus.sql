-- 0029 (0027 on its branch, renumbered after 0027_row_name_checks and 0028_heavy_tier_route_harness): the fleet row's bus store address (pkg/config/kind.go: Kinds,
-- "fleet"; docs/SPEC-BUS.md, the config): host:port of the Redis nova-bus
-- talks to, applied as fleet:bus, what the tool reads when NOVA_BUS_REDIS is
-- unset so no friend types the address. Empty until set.
ALTER TABLE config.fleet ADD COLUMN IF NOT EXISTS
    bus text NOT NULL DEFAULT '';
