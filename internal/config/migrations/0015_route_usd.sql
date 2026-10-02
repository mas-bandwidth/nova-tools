-- 0015: the route's dollar budget per card (internal/config/kind.go: Kinds, "route";
-- nova-tools #5094): the harness's reported cost at which native stops a card, beside
-- the token budget. A decimal kept as text in its one spelling, '' when not set (no
-- cap), never a float, as the price sheet's are (0009), and above 0 when set: a 0 would
-- be dealt onto every card and refused by native at every launch.
ALTER TABLE config.routes
    ADD COLUMN IF NOT EXISTS usd text NOT NULL DEFAULT '' CHECK (
        CASE WHEN usd = '' THEN true
             WHEN usd ~ '^[0-9]+(\.[0-9]+)?$' THEN usd::numeric > 0
             ELSE false END);
