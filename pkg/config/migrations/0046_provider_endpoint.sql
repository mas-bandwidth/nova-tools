-- 0046: a local provider names its engine base URL (pkg/config/kind.go,
-- KindProvider endpoint; docs/SPEC-CONFIG.md, local-config-provider-endpoint).
-- Empty on every other provider. 0045 is friend session, so this is 0046.
-- Run again adds nothing.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'config' AND table_name = 'providers' AND column_name = 'endpoint'
    ) THEN
        ALTER TABLE config.providers ADD COLUMN endpoint text NOT NULL DEFAULT '';
    END IF;
END $$;
