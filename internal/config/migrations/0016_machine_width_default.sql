-- 0016: a machine's width is optional (internal/config/kind.go: Kinds,
-- "machine"; internal/config/width.go). NULL, an unset width, is the default:
-- half the machine's cores as its beat reports them, resolved by nova-sprint
-- fleet sync (the owner, 2026-10-02: "width=-1 in config means default and
-- default is CPUs/2"; the shape: unset means default). A row keeps the width
-- it holds: 0 stays no member, and a number stays that number; nova-config
-- machine set <m> --width default clears one back to unset.
ALTER TABLE config.machines ALTER COLUMN width DROP NOT NULL;
ALTER TABLE config.machines ALTER COLUMN width DROP DEFAULT;
