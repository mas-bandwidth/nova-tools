-- 0028: the local tier in a fleet (internal/config/kind.go: Kinds, "machine" and "route";
-- docs/SPEC-LOCAL.md, "Fleet"). A machine's local_lanes is how many cards the local routes
-- it serves take at once, all of them together; 0, the default, serves none, so every row
-- there before this file serves none until someone sets it. A route's machine names the
-- machine a local route (provider local) is served on (a foreign key: a machine a route
-- names cannot be removed), NULL for every other provider.
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS
    local_lanes integer NOT NULL DEFAULT 0 CHECK (local_lanes >= 0);
ALTER TABLE config.routes ADD COLUMN IF NOT EXISTS
    machine text REFERENCES config.machines (name);
