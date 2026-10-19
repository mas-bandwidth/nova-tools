-- 0047: the sprint row's owner: the name only the owner's writes are recorded
-- under, the one who declares a provider funded (pkg/config/kind.go: Kinds,
-- "sprint", owner; pkg/config/funding.go checkRefuseFunding; docs/SPEC-CONFIG.md,
-- "Funding is the owner's"). Empty for existing rows until the first declaration
-- (nova-config sprint set --owner <name> --actor <name>).
ALTER TABLE config.sprint
    ADD COLUMN IF NOT EXISTS owner text NOT NULL DEFAULT '';
