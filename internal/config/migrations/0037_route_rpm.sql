-- 0037: the route's requests per minute across the whole fleet (internal/config/kind.go,
-- Kinds, "route"; docs/SPEC-CONFIG.md, route): the budget the deal keeps by admitting one lane
-- (sprint.LaneRPM requests a minute) at a time, so a promise made to a provider is kept by the
-- machine and not by the seat. 0 (the default) is unmetered.
ALTER TABLE config.routes
    ADD COLUMN IF NOT EXISTS rpm integer NOT NULL DEFAULT 0 CHECK (rpm >= 0);
