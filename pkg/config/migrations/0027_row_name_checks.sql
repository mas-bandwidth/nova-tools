-- 0027: enforce row name pattern on machines, friends, loops and routes
-- (pkg/config/kind.go: NamePattern ^[a-z0-9][a-z0-9-]*$; docs/SPEC-CONFIG.md,
-- "The schema"; security#69 finding 2).
-- A direct SQL insert could store a name that nova-config add refuses;
-- config.tiers (0008) already constrains its names, so this migration adds
-- a CHECK (name ~ '^[a-z0-9][a-z0-9-]*$') constraint with a stable name to
-- config.machines, config.friends, config.loops and config.routes idempotently.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conrelid = 'config.machines'::regclass
       AND conname = 'machines_name_pattern'
  ) THEN
    ALTER TABLE config.machines ADD CONSTRAINT machines_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$');
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conrelid = 'config.friends'::regclass
       AND conname = 'friends_name_pattern'
  ) THEN
    ALTER TABLE config.friends ADD CONSTRAINT friends_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$');
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conrelid = 'config.loops'::regclass
       AND conname = 'loops_name_pattern'
  ) THEN
    ALTER TABLE config.loops ADD CONSTRAINT loops_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$');
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conrelid = 'config.routes'::regclass
       AND conname = 'routes_name_pattern'
  ) THEN
    ALTER TABLE config.routes ADD CONSTRAINT routes_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$');
  END IF;
END $$;
