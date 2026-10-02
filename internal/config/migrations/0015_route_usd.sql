-- 0015: the route's dollar budget per card (internal/config/kind.go: Kinds, "route";
-- nova-tools #5094): the harness's reported cost at which native stops a card, beside
-- the token budget. A decimal kept as text in its one spelling, '' when not set, never
-- a float, as the price sheet's are (0009).
ALTER TABLE config.routes
    ADD COLUMN IF NOT EXISTS usd text NOT NULL DEFAULT '' CHECK (usd ~ '^([0-9]+(\.[0-9]+)?)?$');
