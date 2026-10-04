-- 0028: the local tier in a fleet (internal/config/kind.go: Kinds, "route";
-- docs/SPEC-LOCAL.md, "Fleet"). A route's endpoint is the http URL a local engine serves
-- its model at (provider local), empty for every other provider; its concurrency is the
-- most cards that may use the route at once, 0 for no cap (the default, so every row there
-- before this file is uncapped). A machine carries no such column: a machine's width is its
-- lanes, and which model serves a lane's calls is the route's.
ALTER TABLE config.routes ADD COLUMN IF NOT EXISTS
    endpoint text NOT NULL DEFAULT '';
ALTER TABLE config.routes ADD COLUMN IF NOT EXISTS
    concurrency integer NOT NULL DEFAULT 0 CHECK (concurrency >= 0);
