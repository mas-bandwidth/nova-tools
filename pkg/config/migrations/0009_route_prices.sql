-- 0009: the route's price sheet (pkg/config/kind.go: Kinds, "route";
-- pkg/cardcost), so what a card cost is worked out from its tokens.
-- Every price is a decimal kept as text in its one spelling, '' when not set,
-- never a float: USD per million tokens for input, cache read, cache write,
-- output and the long prices, USD per request for price_request, a percent
-- for gateway_percent. reasoning_as_output bills reasoning tokens at the
-- output price; long_context is the prompt size above which a request is
-- priced long, 0 none; billing is metered or plan; price_source is free text
-- and price_as_of a date (the CHECKs below, and the kind's Check before them).
ALTER TABLE config.routes
    ADD COLUMN IF NOT EXISTS price_input         text    NOT NULL DEFAULT '' CHECK (price_input ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS price_cache_read    text    NOT NULL DEFAULT '' CHECK (price_cache_read ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS price_cache_write   text    NOT NULL DEFAULT '' CHECK (price_cache_write ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS price_output        text    NOT NULL DEFAULT '' CHECK (price_output ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS reasoning_as_output boolean NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS long_context        integer NOT NULL DEFAULT 0 CHECK (long_context >= 0),
    ADD COLUMN IF NOT EXISTS price_input_long    text    NOT NULL DEFAULT '' CHECK (price_input_long ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS price_output_long   text    NOT NULL DEFAULT '' CHECK (price_output_long ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS price_request       text    NOT NULL DEFAULT '' CHECK (price_request ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS billing             text    NOT NULL DEFAULT 'metered' CHECK (billing IN ('metered', 'plan')),
    ADD COLUMN IF NOT EXISTS gateway_percent     text    NOT NULL DEFAULT '' CHECK (gateway_percent ~ '^([0-9]+(\.[0-9]+)?)?$'),
    ADD COLUMN IF NOT EXISTS price_source        text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS price_as_of         text    NOT NULL DEFAULT '' CHECK (price_as_of ~ '^([0-9]{4}-[0-9]{2}-[0-9]{2})?$');
